package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

func TestPurgeTranscripts_Endpoint(t *testing.T) {
	handler, store, _, shopID := testServerFixture(t)
	ctx := context.Background()

	now := time.Now().UTC()
	oldTime := now.AddDate(0, 0, -45)
	recentTime := now.AddDate(0, 0, -5)

	// 1. Insert an old transaction with transcript
	oldTxn := models.Transaction{
		ID:          "txn_api_old",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo",
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 400000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  oldTime,
		CreatedAt:   oldTime,
		Transcript:  "Two Milo, four thousand naira. Thank you.",
	}
	if err := store.InsertTransaction(ctx, oldTxn); err != nil {
		t.Fatalf("InsertTransaction old: %v", err)
	}

	// 2. Insert a recent transaction with transcript
	recentTxn := models.Transaction{
		ID:          "txn_api_recent",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo",
		Quantity:    1,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 200000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  recentTime,
		CreatedAt:   recentTime,
		Transcript:  "One Milo, two thousand naira. That is all.",
	}
	if err := store.InsertTransaction(ctx, recentTxn); err != nil {
		t.Fatalf("InsertTransaction recent: %v", err)
	}

	// 3. GET should be rejected with 405 Method Not Allowed
	reqGet := httptest.NewRequest(http.MethodGet, "/api/settings/purge-transcripts?shop_id="+shopID, nil)
	rrGet := httptest.NewRecorder()
	handler.ServeHTTP(rrGet, reqGet)
	if rrGet.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rrGet.Code)
	}

	// 4. Request without shop_id should be rejected with 401 Unauthorized
	reqNoAuth := httptest.NewRequest(http.MethodPost, "/api/settings/purge-transcripts", bytes.NewBufferString(`{}`))
	rrNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(rrNoAuth, reqNoAuth)
	if rrNoAuth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rrNoAuth.Code)
	}

	// 5. POST with retention_days: 30
	body := []byte(`{"retention_days": 30}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/purge-transcripts?shop_id="+shopID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp purgeTranscriptsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PurgedCount != 1 {
		t.Errorf("expected purged_count 1, got %d", resp.PurgedCount)
	}
	if resp.OlderThan == "" {
		t.Errorf("expected non-empty older_than in response")
	}

	// 6. Verify in storage that old transcript was purged, while recent is preserved
	gotOld, err := store.GetTransactionByID(ctx, shopID, oldTxn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID old: %v", err)
	}
	if gotOld.Transcript != "" {
		t.Errorf("expected empty transcript on old transaction, got %q", gotOld.Transcript)
	}
	if gotOld.Amount.Amount != 400000 || gotOld.Quantity != 2 {
		t.Errorf("expected transaction financial records to remain intact, got %+v", gotOld)
	}

	gotRecent, err := store.GetTransactionByID(ctx, shopID, recentTxn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID recent: %v", err)
	}
	if gotRecent.Transcript != recentTxn.Transcript {
		t.Errorf("expected recent transcript %q preserved, got %q", recentTxn.Transcript, gotRecent.Transcript)
	}
}

func TestPurgeTranscripts_InvalidTimestamp(t *testing.T) {
	handler, _, _, shopID := testServerFixture(t)

	body := []byte(`{"older_than": "not-valid-rfc3339"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/purge-transcripts?shop_id="+shopID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d: %s", rr.Code, rr.Body.String())
	}
}
