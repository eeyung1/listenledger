package ws

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
)

func TestHub_SequenceNumbers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub(logger)
	go hub.Run()
	defer hub.Shutdown()

	// Initial last sequence should be 0 for any unvisited shop
	if seq := hub.LastSeq("shop_a"); seq != 0 {
		t.Fatalf("expected lastSeq 0, got %d", seq)
	}

	// Publish to shop_a
	hub.Publish("shop_a", EventTransactionCreated, map[string]string{"id": "txn_1"})
	if seq := hub.LastSeq("shop_a"); seq != 1 {
		t.Fatalf("expected shop_a lastSeq 1, got %d", seq)
	}

	hub.Publish("shop_a", EventInventoryUpdated, map[string]string{"id": "prd_1"})
	if seq := hub.LastSeq("shop_a"); seq != 2 {
		t.Fatalf("expected shop_a lastSeq 2, got %d", seq)
	}

	// Shop B should have independent sequence numbers
	if seq := hub.LastSeq("shop_b"); seq != 0 {
		t.Fatalf("expected shop_b lastSeq 0, got %d", seq)
	}

	hub.Publish("shop_b", EventTransactionCreated, map[string]string{"id": "txn_b1"})
	if seq := hub.LastSeq("shop_b"); seq != 1 {
		t.Fatalf("expected shop_b lastSeq 1, got %d", seq)
	}

	// Verify shop_a is still at 2
	if seq := hub.LastSeq("shop_a"); seq != 2 {
		t.Fatalf("expected shop_a lastSeq 2, got %d", seq)
	}
}

func TestEvent_MarshalsSeq(t *testing.T) {
	evt, err := NewEventWithSeq("shop_1", EventTransactionCreated, 42, map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatal(err)
	}
	if evt.Seq != 42 {
		t.Fatalf("expected seq 42, got %d", evt.Seq)
	}

	encoded, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(encoded, &parsed); err != nil {
		t.Fatal(err)
	}

	if rawSeq, ok := parsed["seq"]; !ok || rawSeq.(float64) != 42 {
		t.Fatalf("expected json seq 42, got %v", rawSeq)
	}
}
