package schematicdatastreamws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// WebSocket configuration constants - defaults
	writeWait            = 10 * time.Second
	defaultPongWait      = 40 * time.Second // Default: 40s to handle load balancer timeouts
	defaultPingPeriod    = 30 * time.Second // Default: send ping every 30s (well under typical 60s LB timeout)
	maxReconnectAttempts = 10
	minReconnectDelay    = 1 * time.Second
	maxReconnectDelay    = 30 * time.Second
)

// Logger interface for logging datastream events
type Logger interface {
	Debug(context.Context, string, ...any)
	Info(context.Context, string, ...any)
	Warn(context.Context, string, ...any)
	Error(context.Context, string, ...any)
}

// MessageHandlerFunc is a function type for handling incoming datastream messages
// Expects parsed DataStreamResp messages
type MessageHandlerFunc func(ctx context.Context, message *DataStreamResp) error

// ConnectionReadyHandlerFunc is a function type for functions that need to be called before connection is considered ready
type ConnectionReadyHandlerFunc func(ctx context.Context) error

// ClientOptions contains configuration for the datastream client
type ClientOptions struct {
	URL                    string // HTTP API URL or WebSocket URL - HTTP URLs will be automatically converted to WebSocket URLs
	ApiKey                 string // Schematic API key for authentication
	MessageHandler         MessageHandlerFunc
	ConnectionReadyHandler ConnectionReadyHandlerFunc
	Logger                 Logger
	MaxReconnectAttempts   int
	MinReconnectDelay      time.Duration
	MaxReconnectDelay      time.Duration
	PingPeriod             time.Duration // How often to send pings (default: 30s)
	PongWait               time.Duration // How long to wait for pong response (default: 40s)
	MessageWorkers         int           // Number of concurrent message handler workers (default: 10)
	MessageQueueSize       int           // Size of message queue buffer (default: 100)
}

// Client represents a Schematic datastream websocket client with automatic reconnection
type Client struct {
	// Configuration
	url                    *url.URL
	headers                http.Header
	logger                 Logger
	messageHandler         MessageHandlerFunc
	connectionReadyHandler ConnectionReadyHandlerFunc
	maxReconnectAttempts   int
	minReconnectDelay      time.Duration
	maxReconnectDelay      time.Duration
	pingPeriod             time.Duration
	pongWait               time.Duration

	// Connection state
	conn        *websocket.Conn
	connected   bool // Connection state
	ready       bool // Datastream client ready state
	connectedMu sync.RWMutex
	readyMu     sync.RWMutex
	writeMu     sync.Mutex

	// Control channels
	done      chan bool
	reconnect chan bool
	errors    chan error

	// Message processing worker pool
	messageQueue chan *DataStreamResp
	workersWg    sync.WaitGroup

	// Context cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// convertAPIURLToWebSocketURL converts an API URL to a WebSocket datastream URL
// Examples:
//
//	https://api.schematichq.com -> wss://datastream.schematichq.com/datastream
//	https://api.staging.example.com -> wss://datastream.staging.example.com/datastream
//	https://custom.example.com -> wss://custom.example.com/datastream
//	http://localhost:8080 -> ws://localhost:8080/datastream
func convertAPIURLToWebSocketURL(apiURL string) (*url.URL, error) {
	parsedURL, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("invalid API URL: %w", err)
	}

	// Convert HTTP schemes to WebSocket schemes
	switch parsedURL.Scheme {
	case "https":
		parsedURL.Scheme = "wss"
	case "http":
		parsedURL.Scheme = "ws"
	default:
		return nil, fmt.Errorf("unsupported scheme: %s (must be http or https)", parsedURL.Scheme)
	}

	// Replace 'api' subdomain with 'datastream' if present
	if parsedURL.Host != "" {
		hostParts := strings.Split(parsedURL.Host, ".")
		if len(hostParts) > 1 && hostParts[0] == "api" {
			hostParts[0] = "datastream"
			parsedURL.Host = strings.Join(hostParts, ".")
		}
	}

	// Add datastream path
	parsedURL.Path = "/datastream"

	return parsedURL, nil
}

// NewClient creates a new datastream websocket client with the given options
func NewClient(options ClientOptions) (*Client, error) {
	if options.URL == "" {
		return nil, fmt.Errorf("URL is required")
	}

	if options.ApiKey == "" {
		return nil, fmt.Errorf("ApiKey is required")
	}

	if options.MessageHandler == nil {
		return nil, fmt.Errorf("MessageHandler is required")
	}

	var parsedURL *url.URL
	var err error

	// Auto-detect if this is an HTTP/HTTPS URL that needs conversion to WebSocket
	if strings.HasPrefix(options.URL, "http://") || strings.HasPrefix(options.URL, "https://") {
		parsedURL, err = convertAPIURLToWebSocketURL(options.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to convert API URL: %w", err)
		}
	} else {
		// Assume it's already a WebSocket URL
		parsedURL, err = url.Parse(options.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid URL: %w", err)
		}
	}

	// Create headers with API key
	headers := http.Header{}
	headers.Set("X-Schematic-Api-Key", options.ApiKey)

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
	if options.PingPeriod == 0 {
		options.PingPeriod = defaultPingPeriod
	}
	if options.PongWait == 0 {
		options.PongWait = defaultPongWait
	}
	if options.MessageWorkers == 0 {
		options.MessageWorkers = 1 // Default to 1 worker to ensure message processing order
	}
	if options.MessageQueueSize == 0 {
		options.MessageQueueSize = 100 // Default queue size of 100 messages
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &Client{
		url:                    parsedURL,
		headers:                headers,
		logger:                 options.Logger,
		messageHandler:         options.MessageHandler,
		connectionReadyHandler: options.ConnectionReadyHandler,
		maxReconnectAttempts:   options.MaxReconnectAttempts,
		minReconnectDelay:      options.MinReconnectDelay,
		maxReconnectDelay:      options.MaxReconnectDelay,
		pingPeriod:             options.PingPeriod,
		pongWait:               options.PongWait,
		done:                   make(chan bool, 1),
		reconnect:              make(chan bool, 1),
		errors:                 make(chan error, 100),
		messageQueue:           make(chan *DataStreamResp, options.MessageQueueSize),
		ctx:                    ctx,
		cancel:                 cancel,
	}

	// Start message processing workers
	for i := 0; i < options.MessageWorkers; i++ {
		client.workersWg.Add(1)
		go client.messageWorker()
	}

	return client, nil
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

// IsReady returns whether the datastream client is ready (connected + initialized)
func (c *Client) IsReady() bool {
	c.readyMu.RLock()
	defer c.readyMu.RUnlock()
	return c.ready && c.IsConnected()
}

// SendMessage sends a message through the WebSocket connection
func (c *Client) SendMessage(message interface{}) error {
	if !c.IsConnected() || c.conn == nil {
		return fmt.Errorf("WebSocket connection is not available!")
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

	// Close message queue to signal workers to stop
	close(c.messageQueue)

	// Wait for all message workers to complete
	c.log("info", "Waiting for message workers to complete")
	c.workersWg.Wait()

	// Close connection and reset states
	c.setReady(false)
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

// messageWorker processes messages from the queue with panic recovery
func (c *Client) messageWorker() {
	defer c.workersWg.Done()
	defer func() {
		if r := recover(); r != nil {
			c.log("error", fmt.Sprintf("panic in message worker: %v", r))
			// Try to send error, but don't block if channel is full
			select {
			case c.errors <- fmt.Errorf("panic in message worker: %v", r):
			default:
			}
		}
	}()

	for {
		select {
		case <-c.ctx.Done():
			c.log("debug", "messageWorker: context cancelled, exiting")
			return
		case msg, ok := <-c.messageQueue:
			if !ok {
				// Channel closed, graceful shutdown
				c.log("debug", "messageWorker: queue closed, exiting")
				return
			}

			// Process the message with timeout context awareness
			if err := c.messageHandler(c.ctx, msg); err != nil {
				c.log("error", fmt.Sprintf("messageWorker: handler error: %v", err))
				// Try to send error, but don't block
				select {
				case c.errors <- fmt.Errorf("message handler error: %w", err):
				default:
					c.log("warn", "messageWorker: error channel full, dropping error")
				}
			} else {
				c.log("debug", "messageWorker: message processed successfully")
			}
		}
	}
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
			c.log("info", fmt.Sprintf("Retrying WebSocket connection in %v (attempt %d/%d)", delay, attempts, c.maxReconnectAttempts))

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
		c.log("debug", "Setting up WebSocket pong handler")
		c.conn.SetPongHandler(c.handlePong)

		// Set initial read deadline for ping/pong operations
		c.log("debug", fmt.Sprintf("Setting read deadline for ping/pong: %v", time.Now().Add(c.pongWait)))
		if err := c.conn.SetReadDeadline(time.Now().Add(c.pongWait)); err != nil {
			c.log("error", fmt.Sprintf("Failed to set read deadline: %v", err))
		}

		// Start message reading first so connection is ready to receive responses
		c.log("debug", "Starting message reading goroutine")
		go c.readMessages()

		// Start ping ticker immediately to keep connection alive during initialization
		stopInitPing := c.startInitPingTicker()

		// Call connection ready handler if provided
		if c.connectionReadyHandler != nil {
			c.log("debug", "Calling connection ready handler")
			if err := c.connectionReadyHandler(c.ctx); err != nil {
				c.log("error", fmt.Sprintf("Connection ready handler failed: %v", err))
				c.setConnected(false)
				c.setReady(false)
				stopInitPing() // Stop the temporary ping ticker
				c.conn.Close()
				continue
			}
			c.log("debug", "Connection ready handler completed successfully")
		}

		// Stop the temporary ping ticker - handleConnection will take over
		stopInitPing()

		// Mark as ready only after successful initialization
		c.setReady(true)
		c.log("info", "Datastream client is ready")

		// Handle the connection lifecycle
		c.log("debug", "Starting connection lifecycle management")
		if closed := c.handleConnection(); closed {
			c.log("debug", "Connection closed normally")
			c.setConnected(false)
			return
		}

		c.log("info", "Reconnecting to WebSocket...")
	}
}

// connect establishes the WebSocket connection
func (c *Client) connect() (*websocket.Conn, error) {
	c.log("debug", fmt.Sprintf("connect: attempting to dial WebSocket URL: %s", c.url.String()))

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 30 * time.Second // Set connection timeout

	conn, resp, err := dialer.Dial(c.url.String(), c.headers)

	if err != nil {
		c.log("error", fmt.Sprintf("connect: failed to dial WebSocket: %v", err))
		if resp != nil {
			c.log("debug", fmt.Sprintf("connect: HTTP response status: %s", resp.Status))
		}
		return conn, err
	}

	c.log("info", fmt.Sprintf("connect: successfully established WebSocket connection to %s", c.url.String()))
	if resp != nil {
		c.log("debug", fmt.Sprintf("connect: HTTP response status: %s", resp.Status))
	}

	return conn, err
}

// handleConnection manages the connection lifecycle with ping/pong and reconnection logic
func (c *Client) handleConnection() bool {
	ticker := time.NewTicker(c.pingPeriod)
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
			c.log("debug", "readMessages: context done, stopping message reading")
			return
		default:
		}

		c.log("debug", "readMessages: waiting for WebSocket message...")
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.log("error", fmt.Sprintf("readMessages: failed to read WebSocket message: %v", err))
			c.handleReadError(err)
			return
		}

		// Parse the datastream message
		var message DataStreamResp
		if err := json.Unmarshal(data, &message); err != nil {
			c.log("error", fmt.Sprintf("readMessages: failed to parse datastream message: %v, raw data: %s", err, string(data)))
			c.errors <- fmt.Errorf("failed to parse datastream message: %w", err)
			continue
		}

		var entityID string
		if message.EntityID != nil {
			entityID = *message.EntityID
		}
		c.log("debug", fmt.Sprintf("readMessages: parsed message - EntityType: %s, MessageType: %s, EntityID: %v, DataLength: %d",
			message.EntityType, message.MessageType, entityID, len(message.Data)))

		// Queue message for worker pool processing
		// Use non-blocking send with context awareness
		select {
		case c.messageQueue <- &message:
			c.log("debug", "readMessages: message queued for processing")
		case <-c.ctx.Done():
			c.log("debug", "readMessages: context cancelled while queuing message")
			return
		default:
			c.log("error", "readMessages: message queue full, dropping message")
			c.errors <- fmt.Errorf("message queue full, message dropped")
		}
	}
}

// handleReadError processes errors from reading WebSocket messages
func (c *Client) handleReadError(err error) {
	c.log("debug", fmt.Sprintf("handleReadError: processing WebSocket read error: %v", err))

	// Set disconnected state for all error types
	c.setConnected(false)

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		c.log("debug", fmt.Sprintf("handleReadError: network operation error detected: %v", opErr))
		c.log("debug", "handleReadError: triggering reconnect for network error")
		select {
		case c.reconnect <- true:
			c.log("debug", "handleReadError: reconnect signal sent for network error")
		default:
			c.log("debug", "handleReadError: reconnect channel full, skipping signal")
		}
		return
	}

	// Skip reconnection for normal/graceful closure and signal done
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		c.log("info", fmt.Sprintf("handleReadError: normal WebSocket close detected: %v", err))
		select {
		case c.done <- true:
			c.log("debug", "handleReadError: done signal sent for normal closure")
		default:
			c.log("debug", "handleReadError: done channel full, skipping signal")
		}
		return
	}

	// Skip reconnection for application-level non-retriable errors (e.g. 4001 unauthorized)
	if websocket.IsCloseError(err, 4001) {
		c.log("error", fmt.Sprintf("handleReadError: non-retriable close error: %v", err))
		c.errors <- fmt.Errorf("non-retriable WebSocket close: %w", err)
		select {
		case c.done <- true:
			c.log("debug", "handleReadError: done signal sent for non-retriable error")
		default:
			c.log("debug", "handleReadError: done channel full, skipping signal")
		}
		return
	}

	// For any other error (including abnormal closure 1006), trigger reconnection
	if websocket.IsCloseError(err, websocket.CloseAbnormalClosure) {
		c.log("info", fmt.Sprintf("WebSocket connection lost (abnormal closure, likely timeout): %v", err))
	} else {
		c.log("debug", fmt.Sprintf("handleReadError: triggering reconnect for error: %v", err))
	}

	select {
	case c.reconnect <- true:
		c.log("debug", "handleReadError: reconnect signal sent")
	default:
		c.log("debug", "handleReadError: reconnect channel full, skipping signal")
	}
}

// startInitPingTicker starts a temporary ping ticker during initialization
// Returns a stop function that should be called when initialization completes
func (c *Client) startInitPingTicker() func() {
	c.log("debug", "Starting ping ticker to maintain connection during initialization")
	pingTicker := time.NewTicker(c.pingPeriod)
	pingDone := make(chan bool)

	go func() {
		for {
			select {
			case <-pingDone:
				pingTicker.Stop()
				return
			case <-pingTicker.C:
				if err := c.sendPing(); err != nil {
					c.log("error", fmt.Sprintf("Failed to send ping during init: %v", err))
				}
			}
		}
	}()

	// Return stop function
	return func() {
		close(pingDone)
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
	return c.conn.SetReadDeadline(time.Now().Add(c.pongWait))
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

	// If disconnected, also set ready to false (avoid circular calls)
	if !connected {
		c.readyMu.Lock()
		c.ready = false
		c.readyMu.Unlock()
	}
}

// setReady updates the ready state thread-safely
func (c *Client) setReady(ready bool) {
	c.readyMu.Lock()
	defer c.readyMu.Unlock()
	c.ready = ready
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
