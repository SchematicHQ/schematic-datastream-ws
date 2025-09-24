# Schematic Datastream Client

A high-level Go client for connecting to Schematic's real-time datastream. Abstracts away WebSocket complexity and provides a simple, clean API for receiving live updates about companies, users, and feature flags.

## Features

- **Automatic Connection Management**: Handles WebSocket upgrades, reconnections, and connection lifecycle
- **Real-time Data Updates**: Receive live updates for companies, users, and feature flags
- **Production Ready**: Exponential backoff, jitter, comprehensive error handling
- **Thread-Safe**: Safe for concurrent use across multiple goroutines
- **Flexible Integration**: Function-based handlers, optional logging and initialization callbacks

## Installation

```bash
go get github.com/schematichq/schematic-datastream-ws
```

## Usage

### Basic Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"
    
    schematicdatastreamws "github.com/schematichq/schematic-datastream-ws"
)

// Implement the MessageHandler interface
type MyMessageHandler struct{}

func (h *MyMessageHandler) HandleMessage(ctx context.Context, messageType int, data []byte) error {
    fmt.Printf("Received message: %s\n", string(data))
    return nil
}

// Implement the ConnectionReadyHandler interface (optional)
type MyConnectionReadyHandler struct{}

func (h *MyConnectionReadyHandler) OnConnectionReady(ctx context.Context) error {
    fmt.Println("Connection is ready!")
    // Perform any initialization logic here
    return nil
}

// Simple logger implementation
type SimpleLogger struct{}

func (l *SimpleLogger) Debug(ctx context.Context, msg string) { log.Printf("DEBUG: %s", msg) }
func (l *SimpleLogger) Info(ctx context.Context, msg string)  { log.Printf("INFO: %s", msg) }
func (l *SimpleLogger) Warn(ctx context.Context, msg string)  { log.Printf("WARN: %s", msg) }
func (l *SimpleLogger) Error(ctx context.Context, msg string) { log.Printf("ERROR: %s", msg) }

func main() {
    // Configure the datastream client
    options := schematicdatastreamws.ClientOptions{
        URL:                    "https://api.schematichq.com", // HTTP URLs are automatically converted to WebSocket
        ApiKey:                 "your-schematic-api-key-here",
        MessageHandler:         &MyMessageHandler{},
        ConnectionReadyHandler: &MyConnectionReadyHandler{},
        Logger:                 &SimpleLogger{},
        MaxReconnectAttempts:   10,
        MinReconnectDelay:      time.Second,
        MaxReconnectDelay:      30 * time.Second,
    }

    // Create the client
    client, err := schematicdatastreamws.NewClient(options)
    if err != nil {
        log.Fatalf("Failed to create client: %v", err)
    }

    // Start the connection
    client.Start()

    // Wait for connection
    for !client.IsConnected() {
        time.Sleep(100 * time.Millisecond)
    }

    // Send a message
    message := map[string]interface{}{
        "type": "subscribe",
        "keys": []string{"feature1", "feature2"},
    }
    
    if err := client.SendMessage(message); err != nil {
        log.Printf("Failed to send message: %v", err)
    }

    // Listen for errors
    go func() {
        for err := range client.GetErrorChannel() {
            log.Printf("Connection error: %v", err)
        }
    }()

    // Keep the application running
    select {}
}
```

### Advanced Usage with Custom Logic

```go
// Advanced message handler with JSON parsing
type AdvancedMessageHandler struct {
    cache map[string]interface{}
    mu    sync.RWMutex
}

func (h *AdvancedMessageHandler) HandleMessage(ctx context.Context, messageType int, data []byte) error {
    var message map[string]interface{}
    if err := json.Unmarshal(data, &message); err != nil {
        return fmt.Errorf("failed to parse message: %w", err)
    }

    // Handle different message types
    switch message["type"] {
    case "flag_update":
        h.handleFlagUpdate(message)
    case "heartbeat":
        h.handleHeartbeat(message)
    default:
        log.Printf("Unknown message type: %v", message["type"])
    }

    return nil
}

func (h *AdvancedMessageHandler) handleFlagUpdate(message map[string]interface{}) {
    h.mu.Lock()
    defer h.mu.Unlock()
    
    if key, ok := message["key"].(string); ok {
        h.cache[key] = message["value"]
        log.Printf("Updated flag %s: %v", key, message["value"])
    }
}

func (h *AdvancedMessageHandler) handleHeartbeat(message map[string]interface{}) {
    log.Printf("Received heartbeat: %v", message["timestamp"])
}

// Connection ready handler that performs initialization
type InitializationHandler struct {
    subscriptionKeys []string
    client           *schematicdatastreamws.Client
}

func (h *InitializationHandler) OnConnectionReady(ctx context.Context) error {
    // Subscribe to required keys
    message := map[string]interface{}{
        "type": "subscribe",
        "keys": h.subscriptionKeys,
    }
    
    return h.client.SendMessage(message)
}
```

## Configuration Options

### ClientOptions

- **URL** (required): API endpoint URL (HTTP URLs are automatically converted to WebSocket datastream URLs)
- **ApiKey** (required): Schematic API key for authentication
- **MessageHandler** (required): Function for handling incoming datastream messages
- **ConnectionReadyHandler** (optional): Function for initialization logic after connection
- **Logger** (optional): Interface for logging events
- **MaxReconnectAttempts**: Maximum number of reconnection attempts (default: 10)
- **MinReconnectDelay**: Minimum delay between reconnection attempts (default: 1s)
- **MaxReconnectDelay**: Maximum delay between reconnection attempts (default: 30s)

### Default Values

- Write timeout: 10 seconds
- Pong timeout: 60 seconds
- Ping interval: 54 seconds (90% of pong timeout)
- Exponential backoff with jitter for reconnection delays

## Interfaces

### MessageHandler

Implement this interface to handle incoming WebSocket messages:

```go
type MessageHandler interface {
    HandleMessage(ctx context.Context, messageType int, data []byte) error
}
```

### ConnectionReadyHandler

Implement this interface to perform initialization when the connection is established:

```go
type ConnectionReadyHandler interface {
    OnConnectionReady(ctx context.Context) error
}
```

### Logger

Implement this interface for custom logging:

```go
type Logger interface {
    Debug(ctx context.Context, msg string)
    Info(ctx context.Context, msg string)
    Warn(ctx context.Context, msg string)
    Error(ctx context.Context, msg string)
}
```

## Error Handling

The client provides an error channel for monitoring connection issues:

```go
errorChan := client.GetErrorChannel()
for err := range errorChan {
    // Handle connection errors
    log.Printf("WebSocket error: %v", err)
}
```

## Thread Safety

The client is designed to be thread-safe. You can safely call methods from multiple goroutines:

- `IsConnected()`: Check connection status
- `SendMessage()`: Send messages to the server
- `Close()`: Gracefully close the connection

## Graceful Shutdown

Always close the client when your application shuts down:

```go
defer client.Close()
```

This will:
1. Cancel all internal goroutines
2. Close the WebSocket connection
3. Clean up resources

## License

This project is licensed under the MIT License.
