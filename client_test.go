package schematicdatastreamws

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestClient creates a minimal Client suitable for unit testing handleReadError.
func newTestClient() *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		done:         make(chan bool, 1),
		reconnect:    make(chan bool, 1),
		errors:       make(chan error, 100),
		messageQueue: make(chan *DataStreamResp, 10),
		ctx:          ctx,
		cancel:       cancel,
		connected:    true,
		ready:        true,
	}
}

func TestHandleReadError_NormalClosure(t *testing.T) {
	c := newTestClient()

	err := &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "normal"}
	c.handleReadError(err)

	// Should signal done, not reconnect
	select {
	case <-c.done:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("expected done signal for normal closure, got none")
	}

	select {
	case <-c.reconnect:
		t.Error("unexpected reconnect signal for normal closure")
	default:
		// expected
	}

	if c.IsConnected() {
		t.Error("expected connected to be false after normal closure")
	}
}

func TestHandleReadError_GoingAway(t *testing.T) {
	c := newTestClient()

	err := &websocket.CloseError{Code: websocket.CloseGoingAway, Text: "going away"}
	c.handleReadError(err)

	select {
	case <-c.done:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("expected done signal for going away closure, got none")
	}

	select {
	case <-c.reconnect:
		t.Error("unexpected reconnect signal for going away closure")
	default:
		// expected
	}
}

func TestHandleReadError_4001NonRetriable(t *testing.T) {
	c := newTestClient()

	err := &websocket.CloseError{Code: 4001, Text: "unauthorized"}
	c.handleReadError(err)

	// Should signal done, not reconnect
	select {
	case <-c.done:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("expected done signal for 4001 error, got none")
	}

	select {
	case <-c.reconnect:
		t.Error("unexpected reconnect signal for 4001 error")
	default:
		// expected
	}

	// Should have sent an error to the errors channel
	select {
	case e := <-c.errors:
		if e == nil {
			t.Error("expected non-nil error on errors channel")
		}
	default:
		t.Error("expected error on errors channel for 4001")
	}
}

func TestHandleReadError_AbnormalClosure(t *testing.T) {
	c := newTestClient()

	err := &websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: "abnormal"}
	c.handleReadError(err)

	// Should trigger reconnect, not done
	select {
	case <-c.reconnect:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("expected reconnect signal for abnormal closure, got none")
	}

	select {
	case <-c.done:
		t.Error("unexpected done signal for abnormal closure")
	default:
		// expected
	}
}

func TestHandleReadError_NetworkError(t *testing.T) {
	c := newTestClient()

	err := &net.OpError{Op: "read", Err: net.ErrClosed}
	c.handleReadError(err)

	// Should trigger reconnect
	select {
	case <-c.reconnect:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("expected reconnect signal for network error, got none")
	}

	select {
	case <-c.done:
		t.Error("unexpected done signal for network error")
	default:
		// expected
	}
}

func TestHandleReadError_OtherCloseError(t *testing.T) {
	c := newTestClient()

	// e.g. 1011 internal error — should reconnect
	err := &websocket.CloseError{Code: websocket.CloseInternalServerErr, Text: "internal"}
	c.handleReadError(err)

	select {
	case <-c.reconnect:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("expected reconnect signal for other close error, got none")
	}

	select {
	case <-c.done:
		t.Error("unexpected done signal for other close error")
	default:
		// expected
	}
}
