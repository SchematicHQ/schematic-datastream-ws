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
	// PageSize opts a subscribe request into a paginated flags snapshot, in
	// flags per message. Presence is the opt-in: without it the snapshot
	// arrives as one message, which is what every client built before
	// pagination expects. A client that asks for pages must read Pagination on
	// the response and wait for HasMore false before treating the snapshot as
	// complete -- applying one page as if it were the whole set drops every
	// flag the other pages carry.
	//
	// Counted in flags rather than bytes, so it bounds a page's flag count and
	// not its size: one flag carrying enough rules can still overflow a frame
	// on its own.
	PageSize *int `json:"page_size,omitempty"`
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
	// Pagination is set only on the messages of a paginated snapshot, so its
	// presence tells a client this message is one page rather than the whole
	// set. Absent everywhere else, including on every message to a client that
	// did not ask for pages.
	Pagination *DataStreamPagination `json:"pagination,omitempty"`
	// StreamID is the server-side stream ID of the underlying message. Clients
	// record the latest value and send it back as ReplayFrom on reconnect.
	// Absent on the initial snapshot / subscription confirmation.
	StreamID *string `json:"stream_id,omitempty"`
}

// DataStreamPagination describes one page of a paginated snapshot.
//
// HasMore is what completion rests on, not Page or Total: a client that instead
// counted its way to Total would wait forever on a snapshot the connection cut
// short, and rechunking the pages server-side later would silently change what
// a page index means. Page and Total are there to be shown and to size a
// buffer, and a client that ignores both still behaves correctly.
type DataStreamPagination struct {
	// HasMore is false on the last page of the snapshot.
	HasMore bool `json:"has_more"`
	// Page is this page's 1-based index.
	Page int `json:"page"`
	// Total is how many entities the whole snapshot carries, across all pages.
	Total int `json:"total"`
}

// DataStreamError represents an error message from the datastream
type DataStreamError struct {
	Error      string            `json:"error"`
	Keys       map[string]string `json:"keys,omitempty"`
	EntityType *EntityType       `json:"entity_type,omitempty"`
}
