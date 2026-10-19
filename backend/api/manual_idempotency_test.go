package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/ws"
)

// countingPublisher records every event a handler broadcasts, so a test can
// prove that a replayed write stays silent instead of double-counting on the
// dashboards.
type countingPublisher struct {
	mu     sync.Mutex
	events []ws.EventType
}

func (c *countingPublisher) Publish(_ string, eventType ws.EventType, _ any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, eventType)
	return true
}

func (c *countingPublisher) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

func postManual(t *testing.T, handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/manual", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeManual(t *testing.T, rec *httptest.ResponseRecorder) struct {
	Transaction models.Transaction `json:"transaction"`
	Product     *models.Product    `json:"product"`
} {
	t.Helper()
	var res struct {
		Transaction models.Transaction `json:"transaction"`
		Product     *models.Product    `json:"product"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode manual response: %v", err)
	}
	return res
}

// TestManualTransaction_CommittedRequestWithLostResponseReplays is the P4.8
// regression: the server commits a manual sale but the cashier's device never
// sees the response. When the entry is retried with the same client-minted
// request_id, exactly one ledger entry and one stock movement must exist.
func TestManualTransaction_CommittedRequestWithLostResponseReplays(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	milo, err := store.GetProductBySKU(ctx, shopID, "MILO-500")
	if err != nil {
		t.Fatal(err)
	}
	initialQty := milo.Quantity // 20

	body := `{"request_id":"man_lost_resp_1","type":"sale","product_id":"` + milo.ID + `","quantity":2,"unit":"pack","notes":"Cashier counter fallback"}`

	// First attempt: commits and returns 201.
	first := postManual(t, handler, body, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on first attempt, got %d: %s", first.Code, first.Body.String())
	}
	firstRes := decodeManual(t, first)
	if firstRes.Transaction.ID == "" {
		t.Fatal("expected a transaction id in the first response")
	}

	afterFirst, err := store.GetProductByID(ctx, shopID, milo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst.Quantity != initialQty-2 {
		t.Fatalf("expected stock %d after first attempt, got %d", initialQty-2, afterFirst.Quantity)
	}

	// The response was lost, so the device retries the identical request. The
	// server must replay the committed transaction, not record a second one.
	replay := postManual(t, handler, body, nil)
	if replay.Code != http.StatusOK {
		t.Fatalf("expected 200 OK replay on retry, got %d: %s", replay.Code, replay.Body.String())
	}
	replayRes := decodeManual(t, replay)
	if replayRes.Transaction.ID != firstRes.Transaction.ID {
		t.Fatalf("retry recorded a new transaction: got %s want %s", replayRes.Transaction.ID, firstRes.Transaction.ID)
	}

	afterReplay, err := store.GetProductByID(ctx, shopID, milo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReplay.Quantity != initialQty-2 {
		t.Fatalf("retry double-recorded stock: expected %d, got %d", initialQty-2, afterReplay.Quantity)
	}

	// Exactly one stock movement for the entry, and one ledger row for the key.
	movements, total, err := store.GetProductMovements(ctx, shopID, milo.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(movements) != 1 {
		t.Fatalf("expected exactly one stock movement, got total=%d len=%d", total, len(movements))
	}
	if movements[0].TransactionID != firstRes.Transaction.ID {
		t.Fatalf("movement belongs to unexpected transaction %s", movements[0].TransactionID)
	}
}

// A retry of a committed entry whose response was lost must not re-broadcast the
// inventory/transaction events; a dashboard would otherwise double-count it.
func TestManualTransaction_ReplayedRequestSuppressesEvents(t *testing.T) {
	store, _, shopID := setupCaptureTest(t)
	ctx := context.Background()

	publisher := &countingPublisher{}
	handler := api.New(store, publisher, func(*http.Request) (string, error) { return shopID, nil }, nil)

	milo, err := store.GetProductBySKU(ctx, shopID, "MILO-500")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"request_id":"man_lost_resp_events","type":"sale","product_id":"` + milo.ID + `","quantity":1,"unit":"pack"}`

	if rec := postManual(t, handler, body, nil); rec.Code != http.StatusCreated {
		t.Fatalf("first attempt failed: %d %s", rec.Code, rec.Body.String())
	}
	afterFirst := publisher.count()
	if afterFirst == 0 {
		t.Fatal("expected the first write to broadcast events")
	}

	if rec := postManual(t, handler, body, nil); rec.Code != http.StatusOK {
		t.Fatalf("expected replay 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := publisher.count(); got != afterFirst {
		t.Fatalf("replay re-broadcast events: count went from %d to %d", afterFirst, got)
	}
}

// Reusing a request_id for genuinely different content is a client bug, not a
// retry: it must be refused rather than silently applied.
func TestManualTransaction_SameRequestIDDifferentContentConflicts(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)

	milo, err := store.GetProductBySKU(context.Background(), shopID, "MILO-500")
	if err != nil {
		t.Fatal(err)
	}

	first := postManual(t, handler, `{"request_id":"man_conflict_1","type":"sale","product_id":"`+milo.ID+`","quantity":2,"unit":"pack"}`, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", first.Code, first.Body.String())
	}

	// Same id, different quantity.
	conflict := postManual(t, handler, `{"request_id":"man_conflict_1","type":"sale","product_id":"`+milo.ID+`","quantity":3,"unit":"pack"}`, nil)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for reused request_id with new content, got %d: %s", conflict.Code, conflict.Body.String())
	}

	// The original movement must be untouched by the refused request.
	after, err := store.GetProductByID(context.Background(), shopID, milo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Quantity != 18 {
		t.Fatalf("conflicting request changed stock: expected 18, got %d", after.Quantity)
	}
}

// The request id is mandatory; without one there is no way to deduplicate a
// retry, so the endpoint must refuse rather than silently mint a fresh key.
func TestManualTransaction_RequiresStableRequestID(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)

	milo, err := store.GetProductBySKU(context.Background(), shopID, "MILO-500")
	if err != nil {
		t.Fatal(err)
	}

	missing := postManual(t, handler, `{"type":"sale","product_id":"`+milo.ID+`","quantity":2,"unit":"pack"}`, nil)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when request_id is absent, got %d: %s", missing.Code, missing.Body.String())
	}

	oversize := postManual(t, handler, `{"request_id":"`+strings.Repeat("a", 129)+`","type":"sale","product_id":"`+milo.ID+`","quantity":2,"unit":"pack"}`, nil)
	if oversize.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an over-long request_id, got %d: %s", oversize.Code, oversize.Body.String())
	}
}

// For parity with the capture endpoint, an Idempotency-Key header is accepted as
// a fallback identifier and replays the same way.
func TestManualTransaction_HeaderIdempotencyKeyReplays(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)

	milo, err := store.GetProductBySKU(context.Background(), shopID, "MILO-500")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"type":"sale","product_id":"` + milo.ID + `","quantity":2,"unit":"pack"}`
	headers := map[string]string{"Idempotency-Key": "man_header_key_1"}

	first := postManual(t, handler, body, headers)
	if first.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", first.Code, first.Body.String())
	}
	replay := postManual(t, handler, body, headers)
	if replay.Code != http.StatusOK {
		t.Fatalf("expected 200 OK replay via header key, got %d: %s", replay.Code, replay.Body.String())
	}
	if decodeManual(t, first).Transaction.ID != decodeManual(t, replay).Transaction.ID {
		t.Fatal("header-key replay recorded a different transaction")
	}
}
