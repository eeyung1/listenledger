package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/validate"
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

func TestCaptures_MobileBrowserSessionDirectIntake(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	// Direct browser session intake without device token
	saleJSON := `{
		"capture_id": "cap_mob_001",
		"transcript": "Two Milo, four thousand naira. Thank you.",
		"type": "sale"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(saleJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ListenLedger-Request", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for mobile session capture intake, got %d: %s", rec.Code, rec.Body.String())
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
		t.Errorf("expected clean Milo sale not to require review")
	}
	if len(capRes.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(capRes.Transactions))
	}
	if capRes.Transactions[0].Status != models.StatusConfirmed {
		t.Errorf("expected confirmed status, got %s", capRes.Transactions[0].Status)
	}

	// Verify Milo stock decreased from 20 to 18
	milo, err := store.GetProductByID(ctx, shopID, "prd_milo")
	if err != nil {
		t.Fatalf("GetProductByID: %v", err)
	}
	if milo.Quantity != 18 {
		t.Errorf("expected Milo stock 18, got %d", milo.Quantity)
	}
}

func TestCaptures_NigerianMarketColloquialIntake(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	// Add Indomie and Garri products to catalogue
	extraProducts := []models.Product{
		{
			ID:       "prd_indomie",
			ShopID:   shopID,
			Name:     "Indomie Instant Noodles",
			SKU:      "IND-70G",
			Aliases:  []string{"Indomie", "Indomie noodles"},
			Price:    models.Money{Amount: 41666, Currency: "NGN"},
			Quantity: 100,
			BaseUnit: "piece",
		},
		{
			ID:       "prd_garri",
			ShopID:   shopID,
			Name:     "Ijebu Garri",
			SKU:      "GARRI-IJEBU",
			Aliases:  []string{"garri", "ijebu garri"},
			Price:    models.Money{Amount: 300000, Currency: "NGN"}, // 3,000 NGN per painter
			Quantity: 50,
			BaseUnit: "painter",
		},
	}
	for _, p := range extraProducts {
		if err := store.InsertProduct(ctx, p); err != nil {
			t.Fatalf("InsertProduct: %v", err)
		}
	}

	tests := []struct {
		name         string
		captureID    string
		transcript   string
		expectedProd string
		expectedQty  int
		expectedUnit models.Unit
		expectedAmt  int64
	}{
		{
			name:         "Suffix k notation (Two k Milo)",
			captureID:    "cap_coll_1",
			transcript:   "Two k Milo thank you",
			expectedProd: "Milo 500g",
			expectedQty:  1,
			expectedUnit: "pack",
			expectedAmt:  200000, // 2,000 NGN in kobo
		},
		{
			name:         "Prefix k-fifty with cartons",
			captureID:    "cap_coll_2",
			transcript:   "Three cartons Milo k-fifty thank you",
			expectedProd: "Milo 500g",
			expectedQty:  3,
			expectedUnit: "carton",
			expectedAmt:  5000000, // 50,000 NGN in kobo
		},
		{
			name:         "Half dozen with two-five shorthand",
			captureID:    "cap_coll_3",
			transcript:   "Half dozen Indomie two-five thank you",
			expectedProd: "Indomie Instant Noodles",
			expectedQty:  6,
			expectedUnit: "piece",
			expectedAmt:  250000, // 2,500 NGN in kobo
		},
		{
			name:         "Traditional market measure painter of garri",
			captureID:    "cap_coll_4",
			transcript:   "Two painters of garri six thousand naira thank you",
			expectedProd: "Ijebu Garri",
			expectedQty:  2,
			expectedUnit: "painter",
			expectedAmt:  600000, // 6,000 NGN in kobo
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]string{
				"capture_id": tc.captureID,
				"transcript": tc.transcript,
			}
			body, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-ListenLedger-Request", "1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
			}

			var capRes struct {
				CaptureID    string               `json:"capture_id"`
				Transactions []models.Transaction `json:"transactions"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &capRes); err != nil {
				t.Fatalf("json unmarshal: %v", err)
			}

			if len(capRes.Transactions) != 1 {
				t.Fatalf("expected 1 transaction, got %d", len(capRes.Transactions))
			}

			txn := capRes.Transactions[0]
			if txn.ProductName != tc.expectedProd {
				t.Errorf("product = %q, want %q", txn.ProductName, tc.expectedProd)
			}
			if txn.Quantity != tc.expectedQty {
				t.Errorf("quantity = %d, want %d", txn.Quantity, tc.expectedQty)
			}
			if txn.Unit != tc.expectedUnit {
				t.Errorf("unit = %q, want %q", txn.Unit, tc.expectedUnit)
			}
			if txn.Amount.Amount != tc.expectedAmt {
				t.Errorf("amount = %d, want %d", txn.Amount.Amount, tc.expectedAmt)
			}
		})
	}
}

func TestCaptures_PilotDiscardsAudioFromStaleClients(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	audioSnippet := "data:audio/webm;codecs=opus;base64,GkXfo59ChoEBQveBAULygQRC84EIQoKEd2VibUKHgQRChYECGFOAZwEAAAAAA"

	captureJSON := fmt.Sprintf(`{
		"capture_id": "cap_audio_001",
		"transcript": "Two Milo, four thousand naira. Thank you.",
		"type": "sale",
		"audio_data": "%s"
	}`, audioSnippet)

	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(captureJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ListenLedger-Request", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var capRes struct {
		CaptureID    string               `json:"capture_id"`
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &capRes); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	if len(capRes.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(capRes.Transactions))
	}

	txn := capRes.Transactions[0]
	if txn.AudioData != "" {
		t.Errorf("pilot response retained raw audio: %q", txn.AudioData)
	}

	// Verify retrieval via GetTransactionByID
	dbTxn, err := store.GetTransactionByID(ctx, shopID, txn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID failed: %v", err)
	}
	if dbTxn.AudioData != "" {
		t.Errorf("pilot storage retained raw audio: %q", dbTxn.AudioData)
	}
}

func TestCaptures_PilotRejectsCreditBeforeLedgerWrite(t *testing.T) {
	for _, payload := range []string{
		`{"transcript":"Two Milo, four thousand naira. Thank you.","is_credit_sale":true}`,
		`{"transcript":"Two Milo, four thousand naira. Thank you.","customer_id":"cus_existing"}`,
		`{"transcript":"Two Milo, four thousand naira. Thank you.","customer_name":"Emeka"}`,
		`{"transcript":"Two Milo on credit, thank you"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			store, handler, shopID := setupCaptureTest(t)
			req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-ListenLedger-Request", "1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected parked credit rejection, got %d: %s", rec.Code, rec.Body.String())
			}
			product, err := store.GetProductByID(context.Background(), shopID, "prd_milo")
			if err != nil || product.Quantity != 20 {
				t.Fatalf("rejected credit changed inventory: %+v, %v", product, err)
			}
			ledger := httptest.NewRecorder()
			handler.ServeHTTP(ledger, httptest.NewRequest(http.MethodGet, "/api/transactions", nil))
			var result struct {
				Transactions []models.Transaction `json:"transactions"`
			}
			if ledger.Code != http.StatusOK {
				t.Fatalf("read ledger: %d", ledger.Code)
			}
			if err := json.Unmarshal(ledger.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Transactions) != 0 {
				t.Fatalf("rejected credit wrote ledger: %+v", result.Transactions)
			}
		})
	}
}

// TestCaptures_ReplayedCaptureIDRecordsOnce pins the P4.3 replay contract: a
// retried POST carrying the same browser-minted capture_id (and the same
// Idempotency-Key) records exactly one ledger entry and one stock movement.
func TestCaptures_ReplayedCaptureIDRecordsOnce(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	// Pair an edge microphone so the capture is authenticated like a real device
	pairReq := httptest.NewRequest(http.MethodPost, "/api/devices", bytes.NewBufferString(`{"name":"Counter Mic Pi"}`))
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
	if err := json.Unmarshal(pairRec.Body.Bytes(), &pairRes); err != nil {
		t.Fatalf("decode pair response: %v", err)
	}

	const captureID = "cap_replay_001"
	body := bytes.NewBufferString(`{
		"capture_id": "cap_replay_001",
		"transcript": "Two Milo, four thousand naira. Thank you."
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", body)
	req.Header.Set("Authorization", "Bearer "+pairRes.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", captureID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for first capture, got %d: %s", rec.Code, rec.Body.String())
	}
	var first struct {
		CaptureID    string               `json:"capture_id"`
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first capture: %v", err)
	}
	if first.CaptureID != captureID {
		t.Fatalf("echoed capture_id mismatch: got %q want %q", first.CaptureID, captureID)
	}
	if len(first.Transactions) != 1 || first.Transactions[0].Status != models.StatusConfirmed {
		t.Fatalf("expected one confirmed transaction, got %+v", first.Transactions)
	}

	milo, err := store.GetProductByID(ctx, shopID, "prd_milo")
	if err != nil {
		t.Fatalf("GetProductByID after first capture: %v", err)
	}
	if milo.Quantity != 18 {
		t.Fatalf("expected Milo stock 18 after first capture, got %d", milo.Quantity)
	}

	// A retry (network timeout response, accidental double-fire) re-POSTs the
	// exact same capture id. The server must replay, not double-record.
	replay := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(`{
		"capture_id": "cap_replay_001",
		"transcript": "Two Milo, four thousand naira. Thank you."
	}`))
	replay.Header.Set("Authorization", "Bearer "+pairRes.Token)
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set("Idempotency-Key", captureID)
	replayRec := httptest.NewRecorder()
	handler.ServeHTTP(replayRec, replay)

	if replayRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK replay for same capture_id, got %d: %s", replayRec.Code, replayRec.Body.String())
	}
	var replayRes struct {
		CaptureID    string               `json:"capture_id"`
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(replayRec.Body.Bytes(), &replayRes); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if len(replayRes.Transactions) != 1 {
		t.Fatalf("expected replay to return the single existing transaction, got %d", len(replayRes.Transactions))
	}
	if replayRes.Transactions[0].ID != first.Transactions[0].ID {
		t.Fatalf("expected the replayed transaction to be the original, got new id %s", replayRes.Transactions[0].ID)
	}

	// Stock and ledger must be unchanged by the replay
	milo, err = store.GetProductByID(ctx, shopID, "prd_milo")
	if err != nil {
		t.Fatalf("GetProductByID after replay: %v", err)
	}
	if milo.Quantity != 18 {
		t.Fatalf("replay double-recorded stock: expected 18, got %d", milo.Quantity)
	}
}

func TestP42AnchorlessCaptureCreatesNoLedgerOrReviewEntries(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	defer store.Close()

	before := validate.AnchorlessSuppressedCount()

	// P4.2: a stray closing phrase with no anchors must not create a review item
	// (or any ledger row).
	body := bytes.NewBufferString(`{
		"capture_id": "cap_stray_1",
		"transcript": "Thank you."
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	var capRes struct {
		CaptureID      string               `json:"capture_id"`
		Transactions   []models.Transaction `json:"transactions"`
		RequiresReview bool                 `json:"requires_review"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &capRes); err != nil {
		t.Fatalf("decode capture response: %v", err)
	}
	if len(capRes.Transactions) != 0 {
		t.Fatalf("expected no transactions for anchor-less chatter, got %d", len(capRes.Transactions))
	}
	if capRes.RequiresReview {
		t.Fatalf("expected no review requirement for anchor-less chatter")
	}

	// Nothing reached the ledger, in any status.
	all, total, err := store.QueryTransactions(context.Background(), shopID, storage.TransactionFilter{Limit: 50})
	if err != nil {
		t.Fatalf("QueryTransactions: %v", err)
	}
	if total != 0 || len(all) != 0 {
		t.Fatalf("expected zero stored transactions, got total=%d rows=%d", total, len(all))
	}

	// A genuine sale on the same shop still records normally afterwards.
	saleBody := bytes.NewBufferString(`{
		"capture_id": "cap_sale_after",
		"transcript": "Two Milo, four thousand naira. Thank you."
	}`)
	req = httptest.NewRequest(http.MethodPost, "/api/captures", saleBody)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for the real sale, got %d: %s", rec.Code, rec.Body.String())
	}
	var saleRes struct {
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saleRes); err != nil {
		t.Fatalf("decode sale response: %v", err)
	}
	if len(saleRes.Transactions) != 1 {
		t.Fatalf("expected the real sale to record one transaction, got %d", len(saleRes.Transactions))
	}

	// The diagnostic metric moved by exactly the one suppressed capture.
	if got := validate.AnchorlessSuppressedCount() - before; got != 1 {
		t.Fatalf("expected exactly one suppressed capture, count moved by %d", got)
	}
}
