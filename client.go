package schematicdatastreamws

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// WebSocket configuration constants
	writeWait            = 10 * time.Second
	pongWait             = 60 * time.Second
	pingPeriod           = (pongWait * 9) / 10
	maxReconnectAttempts = 10
	minReconnectDelay    = 1 * time.Second
	maxReconnectDelay    = 30 * time.Second
)

// Logger interface for logging WebSocket events
type Logger interface {
	Debug(ctx context.Context, msg string)
	Info(ctx context.Context, msg string)
	Warn(ctx context.Context, msg string)
	Error(ctx context.Context, msg string)
}

// MessageHandler interface for handling incoming WebSocket messages
type MessageHandler interface {
	HandleMessage(ctx context.Context, messageType int, data []byte) error
}

// ConnectionReadyHandler interface for functions that need to be called before connection is considered ready
type ConnectionReadyHandler interface {
	OnConnectionReady(ctx context.Context) error
}

// ClientOptions contains configuration for the WebSocket client
type ClientOptions struct {
	URL                    string
	Headers                http.Header
	MessageHandler         MessageHandler
	ConnectionReadyHandler ConnectionReadyHandler
	Logger                 Logger
	MaxReconnectAttempts   int
	MinReconnectDelay      time.Duration
	MaxReconnectDelay      time.Duration
}

// Client represents a generic WebSocket client with reconnection capabilities
type Client struct {
	// Configuration
	url                    *url.URL
	headers                http.Header
	logger                 Logger
	messageHandler         MessageHandler
	connectionReadyHandler ConnectionReadyHandler
	maxReconnectAttempts   int
	minReconnectDelay      time.Duration
	maxReconnectDelay      time.Duration

	// Connection state
	conn        *websocket.Conn
	connected   bool
	connectedMu sync.RWMutex
	writeMu     sync.Mutex

	// Control channels
	done      chan bool
	reconnect chan bool
	errors    chan error

	// Context cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// NewClient creates a new WebSocket client with the given options
func NewClient(options ClientOptions) (*Client, error) {
	if options.URL == "" {
		return nil, fmt.Errorf("URL is required")
	}

	if options.MessageHandler == nil {
		return nil, fmt.Errorf("MessageHandler is required")
	}

	parsedURL, err := url.Parse(options.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Set defaults
	if options.MaxReconnectAttempts == 0 {
		options.MaxReconnectAttempts = maxReconnectAttempts
	}
	if options.MinReconnectDelay == 0 {
		options.MinReconnectDelay = minReconnectDelay
	}
	if options.MaxReconnectDelay == 0 {
		options.MaxReconnectDelay = maxReconnectDelay
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		url:                    parsedURL,
		headers:                options.Headers,
		logger:                 options.Logger,
		messageHandler:         options.MessageHandler,
		connectionReadyHandler: options.ConnectionReadyHandler,
		maxReconnectAttempts:   options.MaxReconnectAttempts,
		minReconnectDelay:      options.MinReconnectDelay,
		maxReconnectDelay:      options.MaxReconnectDelay,
		done:                   make(chan bool, 1),
		reconnect:              make(chan bool, 1),
		errors:                 make(chan error, 100),
		ctx:                    ctx,
		cancel:                 cancel,
	}, nil
}

// Start begins the WebSocket connection and message handling
func (c *Client) Start() {
	go c.connectAndRead()
}

// IsConnected returns whether the WebSocket is currently connected
func (c *Client) IsConnected() bool {
	c.connectedMu.RLock()
	defer c.connectedMu.RUnlock()
	return c.connected
}

// SendMessage sends a message through the WebSocket connection
func (c *Client) SendMessage(message interface{}) error {
	if !c.IsConnected() || c.conn == nil {
		return fmt.Errorf("WebSocket connection is not available")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	return c.conn.WriteJSON(message)
}

// Close gracefully closes the WebSocket connection
func (c *Client) Close() {
	c.log("info", "Closing WebSocket client")

	// Cancel context to stop all goroutines
	c.cancel()

	// Signal done
	select {
	case c.done <- true:
	default:
	}

	// Close connection
	c.setConnected(false)
	if c.conn != nil {
		c.conn.Close()
	}

	c.log("info", "WebSocket client closed")
}

// GetErrorChannel returns a channel for receiving connection errors
func (c *Client) GetErrorChannel() <-chan error {
	return c.errors
}

// connectAndRead handles the main connection lifecycle
func (c *Client) connectAndRead() {
	defer func() {
		if r := recover(); r != nil {
			c.log("error", fmt.Sprintf("Fatal error in connectAndRead: %v", r))
		}
	}()

	attempts := 0
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		conn, err := c.connect()
		if err != nil {
			c.log("error", fmt.Sprintf("Failed to connect to WebSocket: %v", err))
			attempts++
			c.setConnected(false)

			if attempts >= c.maxReconnectAttempts {
				c.log("error", "Max reconnection attempts reached")
				c.errors <- fmt.Errorf("max reconnection attempts reached")
				return
			}

			delay := c.calculateBackoffDelay(attempts)
			c.log("info", fmt.Sprintf("Retrying connection in %v (attempt %d/%d)", delay, attempts, c.maxReconnectAttempts))

			select {
			case <-time.After(delay):
				continue
			case <-c.ctx.Done():
				return
			}
		}

		c.log("info", "Connected to WebSocket")
		attempts = 0
		c.conn = conn
		c.setConnected(true)

		// Set up pong handler
		c.conn.SetPongHandler(c.handlePong)

		// Set initial read deadline
		if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			c.log("error", fmt.Sprintf("Failed to set read deadline: %v", err))
		}

		// Start message reading first so connection is ready to receive responses
		go c.readMessages()

		// Call connection ready handler if provided
		if c.connectionReadyHandler != nil {
			if err := c.connectionReadyHandler.OnConnectionReady(c.ctx); err != nil {
				c.log("error", fmt.Sprintf("Connection ready handler failed: %v", err))
				c.setConnected(false)
				c.conn.Close()
				continue
			}
		}

		// Handle the connection lifecycle
		if closed := c.handleConnection(); closed {
			c.setConnected(false)
			return
		}

		c.log("info", "Reconnecting to WebSocket...")
	}
}

// connect establishes the WebSocket connection
func (c *Client) connect() (*websocket.Conn, error) {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(c.url.String(), c.headers)
	return conn, err
}

// handleConnection manages the connection lifecycle with ping/pong and reconnection logic
func (c *Client) handleConnection() bool {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return true
		case <-c.reconnect:
			return false
		case err := <-c.errors:
			c.log("error", fmt.Sprintf("Connection error: %v", err))
		case <-ticker.C:
			if err := c.sendPing(); err != nil {
				c.log("error", fmt.Sprintf("Failed to send ping: %v", err))
				c.setConnected(false)
				return false
			}
		case <-c.ctx.Done():
			return true
		}
	}
}

// readMessages handles incoming WebSocket messages
func (c *Client) readMessages() {
	defer func() {
		if r := recover(); r != nil {
			c.errors <- fmt.Errorf("panic in readMessages: %v", r)
		}
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			c.handleReadError(err)
			return
		}

		// Handle the message using the provided handler
		if err := c.messageHandler.HandleMessage(c.ctx, messageType, data); err != nil {
			c.errors <- fmt.Errorf("message handler error: %w", err)
		}
	}
}

// handleReadError processes errors from reading WebSocket messages
func (c *Client) handleReadError(err error) {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		c.setConnected(false)
		return
	}

	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		c.setConnected(false)
		return
	}

	if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
		c.log("debug", fmt.Sprintf("Unexpected close error: %v", err))
		c.setConnected(false)
		select {
		case c.reconnect <- true:
		default:
		}
		return
	}

	c.setConnected(false)
	select {
	case c.reconnect <- true:
	default:
	}
}

// sendPing sends a ping message to keep the connection alive
func (c *Client) sendPing() error {
	if c.conn == nil {
		return fmt.Errorf("no connection available")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait))
}

// handlePong handles pong responses from the server
func (c *Client) handlePong(string) error {
	return c.conn.SetReadDeadline(time.Now().Add(pongWait))
}

// calculateBackoffDelay calculates exponential backoff delay with jitter
func (c *Client) calculateBackoffDelay(attempt int) time.Duration {
	// Add jitter to prevent synchronized reconnection attempts
	jitter := time.Duration(rand.Int63n(int64(c.minReconnectDelay)))

	// Exponential backoff with a cap
	delay := time.Duration(math.Pow(2, float64(attempt-1)))*c.minReconnectDelay + jitter
	if delay > c.maxReconnectDelay {
		delay = c.maxReconnectDelay + jitter
	}
	return delay
}

// setConnected updates the connection state thread-safely
func (c *Client) setConnected(connected bool) {
	c.connectedMu.Lock()
	defer c.connectedMu.Unlock()
	c.connected = connected
}

// log helper function that safely logs messages
func (c *Client) log(level, msg string) {
	if c.logger == nil {
		return
	}

	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	switch level {
	case "debug":
		c.logger.Debug(ctx, msg)
	case "info":
		c.logger.Info(ctx, msg)
	case "warn":
		c.logger.Warn(ctx, msg)
	case "error":
		c.logger.Error(ctx, msg)
	}
}
