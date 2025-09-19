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
	MessageTypeUnknown MessageType = "unknown"
)

// DataStreamReq represents a request message to the datastream
type DataStreamReq struct {
	Action     Action            `json:"action"`
	EntityType EntityType        `json:"entity_type"`
	Keys       map[string]string `json:"keys,omitempty"`
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
}

// DataStreamError represents an error message from the datastream
type DataStreamError struct {
	Error      string            `json:"error"`
	Keys       map[string]string `json:"keys,omitempty"`
	EntityType *EntityType       `json:"entity_type,omitempty"`
}
