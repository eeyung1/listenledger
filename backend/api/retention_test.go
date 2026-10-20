package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/models"
)

func TestRetentionWorker_LifecycleAndRunOnce(t *testing.T) {
	_, store, _, shopID := testServerFixture(t)
	ctx := context.Background()

	cfg := RetentionConfig{
		RetentionDays: 7,
		Interval:      50 * time.Millisecond,
	}
	worker := NewRetentionWorker(store, cfg)

	if worker.RetentionDays() != 7 {
		t.Fatalf("expected 7 retention days, got %d", worker.RetentionDays())
	}

	status := worker.Status()
	if status.RetentionDays != 7 || status.AutoPurgeActive {
		t.Fatalf("unexpected initial status: %+v", status)
	}

	// Insert an old transaction (10 days ago) with transcript and legacy audio
	now := time.Now().UTC()
	oldTxn := models.Transaction{
		ID:          "txn_worker_old",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo",
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 400000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  now.AddDate(0, 0, -10),
		CreatedAt:   now.AddDate(0, 0, -10),
		Transcript:  "Two Milo packs please",
		AudioData:   "data:audio/webm;base64,GkXfo59ChoEBQveBAULygQ8=",
	}
	if err := store.InsertTransaction(ctx, oldTxn); err != nil {
		t.Fatalf("insert oldTxn: %v", err)
	}

	// Insert a recent transaction (2 days ago)
	recentTxn := models.Transaction{
		ID:          "txn_worker_recent",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo",
		Quantity:    1,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 200000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  now.AddDate(0, 0, -2),
		CreatedAt:   now.AddDate(0, 0, -2),
		Transcript:  "One Milo pack",
		AudioData:   "data:audio/webm;base64,GkXfo59ChoEBQveBAULygQ8=",
	}
	if err := store.InsertTransaction(ctx, recentTxn); err != nil {
		t.Fatalf("insert recentTxn: %v", err)
	}

	// Run once manually
	purged, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged row, got %d", purged)
	}

	// Verify old transaction is cleared, recent transaction is preserved
	gotOld, err := store.GetTransactionByID(ctx, shopID, "txn_worker_old")
	if err != nil {
		t.Fatalf("get old txn: %v", err)
	}
	if gotOld.Transcript != "" || gotOld.AudioData != "" {
		t.Errorf("expected old txn transcript/audio blanked, got transcript=%q audio=%q", gotOld.Transcript, gotOld.AudioData)
	}
	if gotOld.Amount.Amount != 400000 || gotOld.Quantity != 2 {
		t.Errorf("financial records corrupted: %+v", gotOld)
	}

	gotRecent, err := store.GetTransactionByID(ctx, shopID, "txn_worker_recent")
	if err != nil {
		t.Fatalf("get recent txn: %v", err)
	}
	if gotRecent.Transcript != "One Milo pack" {
		t.Errorf("recent transcript modified: %q", gotRecent.Transcript)
	}

	// Check status reflects run
	status = worker.Status()
	if status.LastRunAt == nil || status.LastPurgedCount != 1 {
		t.Errorf("status did not record last run properly: %+v", status)
	}

	// Test background loop with context cancellation
	runCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		worker.Start(runCtx)
		close(stopped)
	}()

	time.Sleep(30 * time.Millisecond)
	status = worker.Status()
	if !status.AutoPurgeActive {
		t.Errorf("expected AutoPurgeActive true while running")
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker did not stop within deadline after context cancellation")
	}

	// Test UpdatePolicy
	if err := worker.UpdatePolicy(14, time.Hour); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	if worker.RetentionDays() != 14 {
		t.Fatalf("expected 14 days after update, got %d", worker.RetentionDays())
	}
	if err := worker.UpdatePolicy(-1, 0); err == nil {
		t.Fatal("expected error for negative retention days")
	}
}

func TestRetentionPolicy_HTTPHandlers(t *testing.T) {
	_, store, pub, shopID := testServerFixture(t)
	worker := NewRetentionWorker(store, RetentionConfig{
		RetentionDays: 7,
		Interval:      24 * time.Hour,
	})

	handler := NewWithRetention(store, pub, auth.QueryShopResolver, nil, worker)

	// 1. GET /api/settings/retention-policy without auth returns 401
	reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/settings/retention-policy", nil)
	rrNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(rrNoAuth, reqNoAuth)
	if rrNoAuth.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rrNoAuth.Code)
	}

	// 2. GET /api/settings/retention-policy with valid shop_id
	reqGet := httptest.NewRequest(http.MethodGet, "/api/settings/retention-policy?shop_id="+shopID, nil)
	rrGet := httptest.NewRecorder()
	handler.ServeHTTP(rrGet, reqGet)
	if rrGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rrGet.Code, rrGet.Body.String())
	}

	var getResp retentionPolicyResponse
	if err := json.Unmarshal(rrGet.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if getResp.ShopID != shopID {
		t.Errorf("expected shop_id %q, got %q", shopID, getResp.ShopID)
	}
	if getResp.Policy.RetentionDays != 7 {
		t.Errorf("expected retention_days 7, got %d", getResp.Policy.RetentionDays)
	}

	// 3. PUT /api/settings/retention-policy updates policy
	putBody := []byte(`{"retention_days": 14, "interval": "12h"}`)
	reqPut := httptest.NewRequest(http.MethodPut, "/api/settings/retention-policy?shop_id="+shopID, bytes.NewReader(putBody))
	reqPut.Header.Set("Content-Type", "application/json")
	rrPut := httptest.NewRecorder()
	handler.ServeHTTP(rrPut, reqPut)
	if rrPut.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on PUT, got %d: %s", rrPut.Code, rrPut.Body.String())
	}

	var putResp retentionPolicyResponse
	if err := json.Unmarshal(rrPut.Body.Bytes(), &putResp); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if putResp.Policy.RetentionDays != 14 {
		t.Errorf("expected retention_days 14 after PUT, got %d", putResp.Policy.RetentionDays)
	}
	if putResp.Policy.Interval != "12h0m0s" {
		t.Errorf("expected interval 12h0m0s after PUT, got %q", putResp.Policy.Interval)
	}

	// 4. PUT with invalid values returns 400
	badDays := []byte(`{"retention_days": -5}`)
	reqBadDays := httptest.NewRequest(http.MethodPut, "/api/settings/retention-policy?shop_id="+shopID, bytes.NewReader(badDays))
	reqBadDays.Header.Set("Content-Type", "application/json")
	rrBadDays := httptest.NewRecorder()
	handler.ServeHTTP(rrBadDays, reqBadDays)
	if rrBadDays.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on negative retention days, got %d", rrBadDays.Code)
	}

	badInterval := []byte(`{"interval": "not-a-duration"}`)
	reqBadInterval := httptest.NewRequest(http.MethodPut, "/api/settings/retention-policy?shop_id="+shopID, bytes.NewReader(badInterval))
	reqBadInterval.Header.Set("Content-Type", "application/json")
	rrBadInterval := httptest.NewRecorder()
	handler.ServeHTTP(rrBadInterval, reqBadInterval)
	if rrBadInterval.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on invalid interval, got %d", rrBadInterval.Code)
	}

	// 5. Method not allowed on DELETE
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/settings/retention-policy?shop_id="+shopID, nil)
	rrDel := httptest.NewRecorder()
	handler.ServeHTTP(rrDel, reqDel)
	if rrDel.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", rrDel.Code)
	}
}
