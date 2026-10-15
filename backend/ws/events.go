package ws

import (
	"encoding/json"
	"fmt"
	"time"
)

// ProtocolVersion is carried on every event as "v". The frontend refuses
// events whose version it does not understand, which lets the wire format
// change without silently corrupting a dashboard that is running stale code.
const ProtocolVersion = 1

// EventType names what happened. The frontend maps these onto DOM event names
// (see frontend/src/ws.ts); adding a type here without mapping it there is
// harmless, and an unknown type arriving at an old client is ignored rather
// than treated as an error.
type EventType string

const (
	// EventTransactionCreated announces a newly recorded movement in either
	// direction — a confirmed sale or a confirmed restock.
	EventTransactionCreated EventType = "transaction.created"
	// EventTransactionUpdated announces a change to an existing record, e.g.
	// an owner correcting a reviewed transaction.
	EventTransactionUpdated EventType = "transaction.updated"
	// EventTransactionReviewRequired announces a low-confidence capture that
	// has been held for a human. Its Data carries the same Transaction shape,
	// so the Review Queue can render it without a follow-up fetch.
	EventTransactionReviewRequired EventType = "transaction.review_required"
	// EventInventoryUpdated announces that a product's stock level moved.
	EventInventoryUpdated EventType = "inventory.updated"
	// EventProductCreated announces a new catalogue entry, including one
	// confirmed from an unrecognised voice capture.
	EventProductCreated EventType = "product.created"
	// EventSettingsUpdated announces changes to shop configurations or thresholds.
	EventSettingsUpdated EventType = "settings.updated"
	// EventTargetGatesUpdated announces agreed changes to pilot release target gates.
	EventTargetGatesUpdated EventType = "target_gates.updated"
	// EventStaffTrainingCompleted announces completion of staff training drill session.
	EventStaffTrainingCompleted EventType = "staff_training.completed"
	// EventExpenseCreated announces a newly recorded operating expense.
	EventExpenseCreated EventType = "expense.created"
	// EventExpenseDeleted announces the removal or voiding of an operating expense.
	EventExpenseDeleted EventType = "expense.deleted"
)

// Event is the single envelope for everything the backend pushes.
//
// One envelope rather than a bare payload means a client can route, version
// and reject messages before it understands their contents, and it keeps the
// transport honest about which shop a message belongs to.
type Event struct {
	// Type is what happened.
	Type EventType `json:"type"`
	// Version is the protocol version, always ProtocolVersion for events
	// produced here.
	Version int `json:"v"`
	// Seq is the monotonic per-shop event sequence number, allowing clients
	// to detect gaps and determine event ordering relative to snapshot state.
	Seq int64 `json:"seq,omitempty"`
	// ShopID is the tenant this event belongs to. The hub routes on it, and
	// re-checks it against the receiving client, so an event can never reach
	// another business's dashboard.
	ShopID string `json:"shop_id"`
	// SentAt is the server's send time, in UTC.
	SentAt time.Time `json:"sent_at"`
	// Data is the type-specific payload, pre-marshalled so the hub serialises
	// once per broadcast rather than once per client.
	Data json.RawMessage `json:"data,omitempty"`
}

// NewEvent builds an event with no sequence number.
func NewEvent(shopID string, eventType EventType, payload any) (Event, error) {
	return NewEventWithSeq(shopID, eventType, 0, payload)
}

// NewEventWithSeq builds an event with an explicit monotonic sequence number.
func NewEventWithSeq(shopID string, eventType EventType, seq int64, payload any) (Event, error) {
	if shopID == "" {
		return Event{}, fmt.Errorf("ws: event %s has no shop_id", eventType)
	}

	var data json.RawMessage
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return Event{}, fmt.Errorf("ws: marshal %s payload: %w", eventType, err)
		}
		data = encoded
	}

	return Event{
		Type:    eventType,
		Version: ProtocolVersion,
		Seq:     seq,
		ShopID:  shopID,
		SentAt:  time.Now().UTC(),
		Data:    data,
	}, nil
}

// Publish marshals payload into an event and broadcasts it to shopID.
//
// It is the entry point the HTTP and capture handlers use: a handler records a
// transaction, then calls Publish and does not care who is listening. Publish
// must be called only after the write it describes has committed, so a
// dashboard can never render a transaction that was later rolled back.
//
// The bool reports whether the event was accepted for delivery. False means
// the hub was saturated or shutting down and the event was dropped; callers
// may log it, but should not fail the request over it — dashboards resync
// after a reconnect.
func (h *Hub) Publish(shopID string, eventType EventType, payload any) bool {
	seq := h.nextSeq(shopID)
	event, err := NewEventWithSeq(shopID, eventType, seq, payload)
	if err != nil {
		h.log.Error("ws: build event", "type", eventType, "shop_id", shopID, "error", err)
		return false
	}
	return h.Broadcast(event)
}
