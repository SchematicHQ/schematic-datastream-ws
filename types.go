package schematicdatastreamws

import "encoding/json"

// Datastream message types for WebSocket communication
type Action string

const (
	ActionStart Action = "start"
	ActionStop  Action = "stop"
)

type EntityType string

const (
	EntityTypeCompany   EntityType = "rulesengine.Company"
	EntityTypeCompanies EntityType = "rulesengine.Companies"
	EntityTypeFlag      EntityType = "rulesengine.Flag"
	EntityTypeFlags     EntityType = "rulesengine.Flags"
	EntityTypeUser      EntityType = "rulesengine.User"
	EntityTypeUsers     EntityType = "rulesengine.Users"
)

type MessageType string

const (
	MessageTypeFull    MessageType = "full"
	MessageTypePartial MessageType = "partial"
	MessageTypeDelete  MessageType = "delete"
	MessageTypeError   MessageType = "error"
	// MessageTypeReload tells the client its replay window has aged out of the
	// server's retention and it must drop local state and do a full reload
	// rather than trust a partial replay. Carries no data.
	MessageTypeReload  MessageType = "reload"
	MessageTypeUnknown MessageType = "unknown"
)

// DataStreamReq represents a request message to the datastream
type DataStreamReq struct {
	Action     Action            `json:"action"`
	EntityType EntityType        `json:"entity_type"`
	Keys       map[string]string `json:"keys,omitempty"`
	// ReplayFrom, when set on a subscribe request, asks the server to replay the
	// messages published after this stream ID (the StreamID of the last message
	// the client processed) so a reconnecting client can catch up on missed
	// changes instead of doing a full reload. If the server can no longer
	// guarantee a complete replay it responds with MessageTypeReload.
	ReplayFrom *string `json:"replay_from,omitempty"`
}

// DataStreamBaseReq wraps the request data
type DataStreamBaseReq struct {
	Data DataStreamReq `json:"data"`
}

// DataStreamResp represents a response message from the datastream
type DataStreamResp struct {
	Data        json.RawMessage `json:"data"`
	EntityID    *string         `json:"entity_id"`
	EntityType  string          `json:"entity_type"`
	MessageType MessageType     `json:"message_type"`
	// StreamID is the server-side stream ID of the underlying message. Clients
	// record the latest value and send it back as ReplayFrom on reconnect.
	// Absent on the initial snapshot / subscription confirmation.
	StreamID *string `json:"stream_id,omitempty"`
}

// DataStreamError represents an error message from the datastream
type DataStreamError struct {
	Error      string            `json:"error"`
	Keys       map[string]string `json:"keys,omitempty"`
	EntityType *EntityType       `json:"entity_type,omitempty"`
}
