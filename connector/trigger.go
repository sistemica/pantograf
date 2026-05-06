package connector

import (
	"context"
	"net/http"
	"time"
)

// TriggerStrategy classifies how a Trigger receives events. It is
// informational — the runtime treats Polling and Push the same (call
// Subscribe and stream events) but a Webhook trigger needs the runtime to
// host an HTTP receiver, which is a different call site.
type TriggerStrategy string

const (
	TriggerPolling TriggerStrategy = "polling" // long-poll/short-poll the API
	TriggerPush    TriggerStrategy = "push"    // server-pushed (IMAP IDLE, SSE)
	TriggerWebhook TriggerStrategy = "webhook" // remote POSTs to our endpoint
)

// Event is one delivery from a Trigger. ID must be stable per source-side
// event so consumers can dedupe across reconnects (Telegram update_id,
// IMAP UID, GitHub delivery-id, ...). Type names a sub-kind ("message",
// "edited_message"). Payload is arbitrary connector-typed data — usually
// a struct or map[string]any that JSON-encodes cleanly.
type Event struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Payload   any       `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

// Sink receives events from a Trigger. The Trigger calls Sink for each
// event in order. Sink may return an error to abort the subscription;
// the Trigger should propagate it from Subscribe. Sink is safe to call
// from any goroutine — the Trigger is responsible for serialising its
// own emissions.
type Sink func(ctx context.Context, e Event) error

// Trigger is one subscribable event source on a Connector. Triggers are
// the second axis of the contract, alongside Actions: Actions are
// caller-driven RPCs, Triggers are source-driven streams.
//
// Triggers MUST additionally implement either StreamingTrigger (for
// Polling/Push strategies) or WebhookTrigger (for Webhook strategy). The
// runtime type-asserts at watch/serve time and surfaces a useful error
// if the strategy and methods don't match.
type Trigger interface {
	Name() string
	DisplayName() string
	Description() string
	Schema() Schema
	Strategy() TriggerStrategy
}

// StreamingTrigger is implemented by Polling and Push-strategy triggers.
// Subscribe blocks until ctx is cancelled or an unrecoverable error.
// Implementations own their loop, retry/backoff, and offset persistence.
type StreamingTrigger interface {
	Trigger
	Subscribe(ctx context.Context, sess Session, params Values, emit Sink) error
}

// WebhookResponse is what a WebhookTrigger.Handle returns to control what
// the runtime writes back to the requester. Returning nil from Handle
// means "200 OK, empty body" — the common case.
type WebhookResponse struct {
	Status      int         // defaults to 200 when zero
	ContentType string      // defaults to text/plain
	Headers     http.Header // additional headers; optional
	Body        []byte
}

// WebhookTrigger is implemented by Webhook-strategy triggers. The runtime
// hosts an HTTP receiver and routes incoming requests to Handle. OnEnable
// runs once at serve time to register with the upstream service (e.g.
// Telegram setWebhook); OnDisable reverses it.
//
// Method, path, query, headers, and body are all available on req.
// Triggers that only care about POSTs should reject other methods
// themselves and return an appropriate WebhookResponse with 405.
type WebhookTrigger interface {
	Trigger
	OnEnable(ctx context.Context, sess Session, params Values, publicURL string) error
	OnDisable(ctx context.Context, sess Session, params Values) error
	Handle(ctx context.Context, sess Session, params Values, req *http.Request, emit Sink) (*WebhookResponse, error)
}
