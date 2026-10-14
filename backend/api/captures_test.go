package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

type dummyPublisher struct{}

func (d *dummyPublisher) Publish(shopID string, eventType any, payload any) bool {
	return true
}

func setupCaptureTest(t *testing.T) (*storage.Store, http.Handler, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	shopID := "shop_cap_1"
	// Create owner and shop in identity table
	account := storage.Account{
		Identity: storage.Identity{
			OwnerID:  "own_cap_1",
			Email:    "owner@example.com",
			ShopID:   shopID,
			ShopName: "Corner Shop",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "pass",
		RecoveryHash: "rec",
	}
	if err := store.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Insert test catalogue
	products := []models.Product{
		{
			ID:       "prd_milo",
			ShopID:   shopID,
			Name:     "Milo 500g",
			SKU:      "MILO-500",
			Aliases:  []string{"Milo", "Nestle Milo"},
			Price:    models.Money{Amount: 200000, Currency: "NGN"},
			Quantity: 20,
			BaseUnit: "pack",
			Packaging: []models.PackagingRelation{
				{From: "carton", To: "pack", Factor: 12},
			},
		},
		{
			ID:       "prd_pepsi",
			ShopID:   shopID,
			Name:     "Pepsi 50cl",
			SKU:      "PEP-50",
			Aliases:  []string{"Pepsi", "Pepsi bottle"},
			Price:    models.Money{Amount: 240000, Currency: "NGN"},
			Quantity: 8,
			BaseUnit: "pack",
			Packaging: []models.PackagingRelation{
				{From: "carton", To: "pack", Factor: 12},
			},
		},
	}
	for _, p := range products {
		if err := store.InsertProduct(context.Background(), p); err != nil {
			t.Fatalf("InsertProduct: %v", err)
		}
	}

	resolver := func(r *http.Request) (string, error) {
		return shopID, nil
	}
	handler := api.New(store, nil, resolver, nil)
	return store, handler, shopID
}

func TestCapturesAndDeviceWorkflow(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	defer store.Close()

	// 1. Pair a new edge device
	pairBody := bytes.NewBufferString(`{"name":"Counter Mic Pi"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices", pairBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created pairing device, got %d: %s", rec.Code, rec.Body.String())
	}

	var pairRes struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pairRes); err != nil {
		t.Fatalf("decode pair response: %v", err)
	}
	if pairRes.Token == "" || pairRes.ID == "" {
		t.Fatalf("missing token or id in pair response: %+v", pairRes)
	}

	// 2. Submit clean sale via device token: "Two Milo, four thousand naira. Thank you."
	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_001",
		"transcript": "Two Milo, four thousand naira. Thank you."
	}`)
	req = httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+pairRes.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for capture, got %d: %s", rec.Code, rec.Body.String())
	}

	var capRes struct {
		CaptureID      string               `json:"capture_id"`
		Transactions   []models.Transaction `json:"transactions"`
		RequiresReview bool                 `json:"requires_review"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &capRes); err != nil {
		t.Fatalf("decode capture response: %v", err)
	}
	if capRes.RequiresReview {
		t.Fatalf("expected clean capture not to require review")
	}
	if len(capRes.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(capRes.Transactions))
	}
	if capRes.Transactions[0].Status != models.StatusConfirmed {
		t.Fatalf("expected confirmed transaction, got %s", capRes.Transactions[0].Status)
	}

	// Verify Milo stock decreased from 20 to 18
	milo, err := store.GetProductByID(context.Background(), shopID, "prd_milo")
	if err != nil {
		t.Fatalf("GetProductByID: %v", err)
	}
	if milo.Quantity != 18 {
		t.Fatalf("expected Milo stock 18, got %d", milo.Quantity)
	}

	// 3. Submit uncertain capture: unknown product "Two Golden Morn, five thousand naira. Thank you."
	captureBody = bytes.NewBufferString(`{
		"capture_id": "cap_002",
		"transcript": "Two Golden Morn, five thousand naira. Thank you."
	}`)
	req = httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+pairRes.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for uncertain capture, got %d: %s", rec.Code, rec.Body.String())
	}
	var uncertRes struct {
		RequiresReview bool                 `json:"requires_review"`
		Transactions   []models.Transaction `json:"transactions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &uncertRes)
	if !uncertRes.RequiresReview {
		t.Fatalf("expected uncertain capture to require review")
	}
	if uncertRes.Transactions[0].Status != models.StatusReviewRequired {
		t.Fatalf("expected status review_required, got %s", uncertRes.Transactions[0].Status)
	}

	// 4. Submit multi-line restock: Blueprint §10
	captureBody = bytes.NewBufferString(`{
		"capture_id": "cap_003",
		"transcript": "Five packs of Pepsi, twelve thousand naira. Three cartons of Milo, forty-five thousand naira. Stock received."
	}`)
	req = httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+pairRes.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for restock capture, got %d: %s", rec.Code, rec.Body.String())
	}

	// Check stock updates:
	// Pepsi was 8 -> + 5 = 13
	pepsi, _ := store.GetProductByID(context.Background(), shopID, "prd_pepsi")
	if pepsi.Quantity != 13 {
		t.Fatalf("expected Pepsi stock 13, got %d", pepsi.Quantity)
	}
	// Milo was 18 -> + 3 cartons (x12 packs) = +36 -> 54
	milo, _ = store.GetProductByID(context.Background(), shopID, "prd_milo")
	if milo.Quantity != 54 {
		t.Fatalf("expected Milo stock 54 after restock, got %d", milo.Quantity)
	}

	// 5. Test Server-side Daily Summary (GET /api/summary)
	req = httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for summary, got %d: %s", rec.Code, rec.Body.String())
	}
	var sumRes struct {
		TodaySalesAmount models.Money `json:"today_sales_amount"`
		TodaySalesCount  int          `json:"today_sales_count"`
		PendingReviews   int          `json:"pending_reviews"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sumRes); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if sumRes.TodaySalesCount != 1 {
		t.Fatalf("expected today sales count 1, got %d", sumRes.TodaySalesCount)
	}
	if sumRes.TodaySalesAmount.Amount != 400000 {
		t.Fatalf("expected today sales amount 400000 kobo, got %d", sumRes.TodaySalesAmount.Amount)
	}
	if sumRes.PendingReviews != 1 {
		t.Fatalf("expected pending reviews 1, got %d", sumRes.PendingReviews)
	}

	// 6. Test Device Revocation
	req = httptest.NewRequest(http.MethodPost, "/api/devices/"+pairRes.ID+"/revoke", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK revoking device, got %d: %s", rec.Code, rec.Body.String())
	}

	// 7. Verify subsequent capture with revoked device is rejected with 401
	captureBody = bytes.NewBufferString(`{
		"capture_id": "cap_004",
		"transcript": "Two Milo, four thousand naira. Thank you."
	}`)
	req = httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+pairRes.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for revoked device, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCaptures_MultiLineRestockAndSpokenSaleE2E(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	// Seed Peak Milk product with packaging conversion
	peakMilk := models.Product{
		ID:                "prd_peak",
		ShopID:            shopID,
		Name:              "Peak Evaporated Milk",
		SKU:               "PEAK-01",
		Aliases:           []string{"Peak Milk", "Liquid Peak"},
		Price:             models.Money{Amount: 450000, Currency: "NGN"},
		Quantity:          40,
		BaseUnit:          models.UnitPack,
		LowStockThreshold: 10,
		Packaging: []models.PackagingRelation{
			{From: models.UnitCarton, To: models.UnitPack, Factor: 24},
		},
	}
	if err := store.InsertProduct(ctx, peakMilk); err != nil {
		t.Fatalf("insert peak milk: %v", err)
	}

	// 1. Pair edge microphone
	pairReq := httptest.NewRequest(http.MethodPost, "/api/devices", bytes.NewBufferString(`{"name":"Counter USB Mic"}`))
	pairReq.Header.Set("Content-Type", "application/json")
	pairRec := httptest.NewRecorder()
	handler.ServeHTTP(pairRec, pairReq)

	if pairRec.Code != http.StatusCreated {
		t.Fatalf("pair device failed: %d", pairRec.Code)
	}
	var pairRes struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(pairRec.Body.Bytes(), &pairRes)

	// 2. Submit multi-line restock from paired edge device (3 cartons Peak Milk = 72 packs)
	restockBody := bytes.NewBufferString(`{
		"capture_id": "cap_e2e_restock",
		"type": "restock",
		"transcript": "Three cartons of Peak Milk, one hundred thousand naira. Stock received.",
		"extracted_lines": [
			{
				"product_name": "Peak Milk",
				"quantity": 3,
				"unit": "carton",
				"amount": {"amount": 10000000, "currency": "NGN"}
			}
		]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", restockBody)
	req.Header.Set("Authorization", "Bearer "+pairRes.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("restock capture failed: %d: %s", rec.Code, rec.Body.String())
	}

	// Stock should have increased from 40 to 40 + (3 * 24) = 112
	updatedPeak, err := store.GetProductByID(ctx, shopID, "prd_peak")
	if err != nil {
		t.Fatalf("get peak milk: %v", err)
	}
	if updatedPeak.Quantity != 112 {
		t.Errorf("expected Peak Milk quantity 112 after restock, got %d", updatedPeak.Quantity)
	}

	// 3. Submit spoken sale from paired edge device (2 packs Peak Milk)
	saleJSON := `{
		"capture_id": "cap_e2e_sale",
		"type": "sale",
		"transcript": "Two Peak Milk, nine thousand naira. Thank you.",
		"extracted_lines": [
			{
				"product_name": "Peak Milk",
				"quantity": 2,
				"unit": "pack",
				"amount": {"amount": 900000, "currency": "NGN"}
			}
		]
	}`
	req = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(saleJSON))
	req.Header.Set("Authorization", "Bearer "+pairRes.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("sale capture failed: %d: %s", rec.Code, rec.Body.String())
	}

	// Stock should have decreased from 112 to 110
	updatedPeak, _ = store.GetProductByID(ctx, shopID, "prd_peak")
	if updatedPeak.Quantity != 110 {
		t.Errorf("expected Peak Milk quantity 110 after sale, got %d", updatedPeak.Quantity)
	}

	// 4. Verify idempotent replay returns existing result without double-counting stock
	req = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(saleJSON))
	req.Header.Set("Authorization", "Bearer "+pairRes.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on idempotent retry, got %d: %s", rec.Code, rec.Body.String())
	}
	updatedPeak, _ = store.GetProductByID(ctx, shopID, "prd_peak")
	if updatedPeak.Quantity != 110 {
		t.Errorf("expected Peak Milk quantity to remain 110 on retry, got %d", updatedPeak.Quantity)
	}
}
