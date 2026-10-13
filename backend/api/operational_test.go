package api_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

func TestListTransactionsFilteringAndPaging(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	now := time.Now().UTC()
	t1 := models.Transaction{
		ID:          "txn_1",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo 500g",
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 400000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  now.Add(-2 * time.Hour),
		CreatedAt:   now.Add(-2 * time.Hour),
		Transcript:  "Two Milo, four thousand naira. Thank you.",
	}
	t2 := models.Transaction{
		ID:          "txn_2",
		ShopID:      shopID,
		Type:        models.TypeRestock,
		ProductID:   "prd_milo",
		ProductName: "Milo 500g",
		Quantity:    12,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 2000000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  now.Add(-1 * time.Hour),
		CreatedAt:   now.Add(-1 * time.Hour),
		Transcript:  "One carton Milo, twenty thousand naira. Stock received.",
	}
	t3 := models.Transaction{
		ID:          "txn_3",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo 500g",
		Quantity:    1,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 200000, Currency: "NGN"},
		Status:      models.StatusReviewRequired,
		OccurredAt:  now,
		CreatedAt:   now,
		Transcript:  "One small Milo.",
	}

	for _, txn := range []models.Transaction{t1, t2, t3} {
		if err := store.InsertTransaction(ctx, txn); err != nil {
			t.Fatalf("insert transaction: %v", err)
		}
	}

	// 1. Filter by Type=sale
	req := httptest.NewRequest(http.MethodGet, "/api/transactions?type=sale", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Transactions []models.Transaction `json:"transactions"`
		Total        int                  `json:"total"`
		Count        int                  `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if resp.Total != 2 || resp.Count != 2 {
		t.Fatalf("expected 2 sales, got total=%d count=%d", resp.Total, resp.Count)
	}

	// 2. Filter by Status=review_required
	req = httptest.NewRequest(http.MethodGet, "/api/transactions?status=review_required", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if resp.Total != 1 || resp.Count != 1 || resp.Transactions[0].ID != "txn_3" {
		t.Fatalf("expected txn_3 in review, got %+v", resp)
	}

	// 3. Pagination: limit=1, offset=1
	req = httptest.NewRequest(http.MethodGet, "/api/transactions?limit=1&offset=1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if resp.Total != 3 || resp.Count != 1 || resp.Transactions[0].ID != "txn_2" {
		t.Fatalf("expected offset 1 to return txn_2, got total=%d count=%d txn=%+v", resp.Total, resp.Count, resp.Transactions)
	}
}

func TestExportTransactionsCSVAndFormulaInjectionSanitization(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	// Insert a normal transaction and one with potential CSV injection formulas
	t1 := models.Transaction{
		ID:          "txn_norm",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo 500g",
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 400000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		Transcript:  "Two Milo, four thousand naira. Thank you.",
	}
	t2 := models.Transaction{
		ID:          "txn_formula",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "+Exploit Product",
		Quantity:    1,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 100000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		Transcript:  "@SUM(1,2)",
	}

	if err := store.InsertTransaction(ctx, t1); err != nil {
		t.Fatalf("insert t1: %v", err)
	}
	if err := store.InsertTransaction(ctx, t2); err != nil {
		t.Fatalf("insert t2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/export?format=csv", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	contentType := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("expected text/csv Content-Type, got %q", contentType)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment; filename=\"listenledger-transactions-") {
		t.Fatalf("expected attachment Content-Disposition, got %q", disposition)
	}

	reader := csv.NewReader(rec.Body)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("csv parse error: %v", err)
	}

	if len(records) != 3 { // 1 header row + 2 data rows
		t.Fatalf("expected 3 csv rows, got %d", len(records))
	}

	// Verify headers
	headers := records[0]
	if headers[0] != "Transaction ID" || headers[7] != "Amount" || headers[11] != "Transcript" {
		t.Fatalf("unexpected headers: %v", headers)
	}

	// Find the formula row
	var formulaRow []string
	for _, row := range records[1:] {
		if strings.Contains(row[0], "txn_formula") {
			formulaRow = row
			break
		}
	}
	if formulaRow == nil {
		t.Fatal("formula row not found in CSV")
	}

	// Assert formula injection values were prefixed with single quote '
	productNameCol := formulaRow[4]
	transcriptCol := formulaRow[11]

	if !strings.HasPrefix(productNameCol, "'+") {
		t.Errorf("productName %q was not sanitized against '+' formula injection", productNameCol)
	}
	if !strings.HasPrefix(transcriptCol, "'@") {
		t.Errorf("transcript %q was not sanitized against '@' formula injection", transcriptCol)
	}
}

func TestSettingsWorkflow(t *testing.T) {
	_, handler, _ := setupCaptureTest(t)

	// 1. GET /api/settings
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var settings struct {
		Shop struct {
			Name                string  `json:"name"`
			Currency            string  `json:"currency"`
			Timezone            string  `json:"timezone"`
			ConfidenceThreshold float64 `json:"confidence_threshold"`
			SalePhrase          string  `json:"sale_phrase"`
			RestockPhrase       string  `json:"restock_phrase"`
		} `json:"shop"`
		Devices []storage.Device `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if settings.Shop.Name != "Corner Shop" || settings.Shop.Currency != "NGN" {
		t.Fatalf("unexpected shop initial state: %+v", settings.Shop)
	}

	// 2. PATCH /api/settings
	newName := "Corner Mart Central"
	newThreshold := 0.82
	newSale := "Thanks so much"
	patchBody, _ := json.Marshal(map[string]any{
		"name":                 newName,
		"confidence_threshold": newThreshold,
		"sale_phrase":          newSale,
	})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/settings expected 200, got %d: %s", patchRec.Code, patchRec.Body.String())
	}

	// 3. GET /api/settings again to verify persistence
	req = httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if settings.Shop.Name != newName || settings.Shop.ConfidenceThreshold != newThreshold || settings.Shop.SalePhrase != newSale {
		t.Fatalf("settings not updated: %+v", settings.Shop)
	}

	// 4. Invalid PATCH: threshold < 0.50
	badThresholdBody, _ := json.Marshal(map[string]any{
		"confidence_threshold": 0.25,
	})
	badReq := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(badThresholdBody))
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad threshold, got %d", badRec.Code)
	}
}

func TestRequestIDAndPanicRecovery(t *testing.T) {
	_, handler, _ := setupCaptureTest(t)

	// Verify X-Request-ID response header
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Header.Set("X-Request-ID", "custom_req_123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") != "custom_req_123" {
		t.Errorf("expected echoed X-Request-ID, got %q", rec.Header().Get("X-Request-ID"))
	}

	// Verify Recoverer middleware recovers from handler panics
	panickingHandler := api.Recoverer(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("deliberate test crash")
	}))
	panicReq := httptest.NewRequest(http.MethodGet, "/test-panic", nil)
	panicRec := httptest.NewRecorder()
	panickingHandler.ServeHTTP(panicRec, panicReq)

	if panicRec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on panic, got %d", panicRec.Code)
	}
	var errResp map[string]string
	if err := json.Unmarshal(panicRec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode panic response json: %v", err)
	}
	if errResp["error"] != "internal server error" {
		t.Fatalf("expected internal server error message, got %v", errResp)
	}
}
