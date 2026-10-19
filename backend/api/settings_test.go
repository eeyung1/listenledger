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

func TestUpdateSettings_RecognitionLanguage_P4_4(t *testing.T) {
	handler, _, _, shopID := testServerFixture(t)

	// 1. Default language keeps the pre-P4.4 en-US baseline (P4.4).
	reqGet := httptest.NewRequest(http.MethodGet, "/api/settings?shop_id="+shopID, nil)
	rrGet := httptest.NewRecorder()
	handler.ServeHTTP(rrGet, reqGet)
	if rrGet.Code != http.StatusOK {
		t.Fatalf("GET /api/settings: expected 200, got %d: %s", rrGet.Code, rrGet.Body.String())
	}
	var got settingsResponse
	if err := json.Unmarshal(rrGet.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got.Shop.RecognitionLanguage != "en-US" {
		t.Errorf("expected default recognition_language en-US, got %q", got.Shop.RecognitionLanguage)
	}

	// 2. A valid language tag is persisted and echoed back.
	patchBody := []byte(`{"recognition_language": "yo-NG"}`)
	reqPatch := httptest.NewRequest(http.MethodPatch, "/api/settings?shop_id="+shopID, bytes.NewReader(patchBody))
	reqPatch.Header.Set("Content-Type", "application/json")
	rrPatch := httptest.NewRecorder()
	handler.ServeHTTP(rrPatch, reqPatch)
	if rrPatch.Code != http.StatusOK {
		t.Fatalf("PATCH valid language: expected 200, got %d: %s", rrPatch.Code, rrPatch.Body.String())
	}
	var updated struct {
		RecognitionLanguage string `json:"recognition_language"`
	}
	if err := json.Unmarshal(rrPatch.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode patched shop: %v", err)
	}
	if updated.RecognitionLanguage != "yo-NG" {
		t.Errorf("expected recognition_language yo-NG, got %q", updated.RecognitionLanguage)
	}

	// 3. Invalid and empty tags are rejected with 400 and do not change the value.
	for _, invalid := range []string{`{"recognition_language": "!!"}`, `{"recognition_language": "e"}`, `{"recognition_language": ""}`} {
		reqBad := httptest.NewRequest(http.MethodPatch, "/api/settings?shop_id="+shopID, bytes.NewReader([]byte(invalid)))
		reqBad.Header.Set("Content-Type", "application/json")
		rrBad := httptest.NewRecorder()
		handler.ServeHTTP(rrBad, reqBad)
		if rrBad.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s: expected 400, got %d: %s", invalid, rrBad.Code, rrBad.Body.String())
		}
	}

	reqGet2 := httptest.NewRequest(http.MethodGet, "/api/settings?shop_id="+shopID, nil)
	rrGet2 := httptest.NewRecorder()
	handler.ServeHTTP(rrGet2, reqGet2)
	var after settingsResponse
	if err := json.Unmarshal(rrGet2.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode settings after invalid patches: %v", err)
	}
	if after.Shop.RecognitionLanguage != "yo-NG" {
		t.Errorf("expected recognition_language to remain yo-NG after rejected updates, got %q", after.Shop.RecognitionLanguage)
	}
}

func TestPurgeTranscripts_QueryParameters(t *testing.T) {
	handler, store, _, shopID := testServerFixture(t)
	ctx := context.Background()

	now := time.Now().UTC()
	oldTime := now.AddDate(0, 0, -45)
	recentTime := now.Add(-2 * time.Hour)

	oldTxn := models.Transaction{
		ID:          "txn_qp_old",
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
	recentTxn := models.Transaction{
		ID:          "txn_qp_recent",
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

	// The RUNBOOK command passes retention_days as a query parameter. It must
	// control the cutoff rather than silently falling back to the 30-day default.
	req := httptest.NewRequest(http.MethodPost, "/api/settings/purge-transcripts?shop_id="+shopID+"&retention_days=1", nil)
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
	cutoff, err := time.Parse(time.RFC3339, resp.OlderThan)
	if err != nil {
		t.Fatalf("parse older_than %q: %v", resp.OlderThan, err)
	}
	if age := now.Sub(cutoff); age < 23*time.Hour || age > 25*time.Hour {
		t.Errorf("expected a ~1 day cutoff from retention_days, got %s (age %s)", resp.OlderThan, age)
	}

	gotOld, err := store.GetTransactionByID(ctx, shopID, oldTxn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID old: %v", err)
	}
	if gotOld.Transcript != "" {
		t.Errorf("expected the old transcript to be purged, got %q", gotOld.Transcript)
	}
	gotRecent, err := store.GetTransactionByID(ctx, shopID, recentTxn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID recent: %v", err)
	}
	if gotRecent.Transcript != recentTxn.Transcript {
		t.Errorf("expected the recent transcript to be preserved, got %q", gotRecent.Transcript)
	}

	// A non-numeric retention_days is rejected instead of being ignored.
	badReq := httptest.NewRequest(http.MethodPost, "/api/settings/purge-transcripts?shop_id="+shopID+"&retention_days=soon", nil)
	rrBad := httptest.NewRecorder()
	handler.ServeHTTP(rrBad, badReq)
	if rrBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for a non-numeric retention_days, got %d: %s", rrBad.Code, rrBad.Body.String())
	}
}
