package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	ShopID    string
	EventType ws.EventType
	Payload   any
}

func (r *recordingPublisher) Publish(shopID string, eventType ws.EventType, payload any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{
		ShopID:    shopID,
		EventType: eventType,
		Payload:   payload,
	})
	return true
}

func (r *recordingPublisher) countEvents(eventType ws.EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, e := range r.events {
		if e.EventType == eventType {
			count++
		}
	}
	return count
}

func setupE2EFixture(t *testing.T) (*storage.Store, http.Handler, *recordingPublisher, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "e2e_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	shopID := "shop_e2e_1"
	account := storage.Account{
		Identity: storage.Identity{
			OwnerID:  "own_e2e_1",
			Email:    "e2e@example.com",
			ShopID:   shopID,
			ShopName: "Mama Tolu Provisions",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "pass",
		RecoveryHash: "rec",
	}
	if err := store.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Insert test catalogue: Milo 500g (qty 20, 1 carton = 12 packs) & Pepsi 50cl (qty 8, 1 carton = 12 packs)
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

	pub := &recordingPublisher{}
	resolver := func(r *http.Request) (string, error) {
		return shopID, nil
	}
	handler := api.New(store, pub, resolver, nil)

	// Pair device
	pairBody := bytes.NewBufferString(`{"name":"Counter Edge Mic"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices", pairBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("failed to pair test device: %d: %s", rec.Code, rec.Body.String())
	}
	var pairRes struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pairRes); err != nil {
		t.Fatalf("decode pair response: %v", err)
	}

	return store, handler, pub, shopID, pairRes.Token
}

// 1. Sale capture end-to-end with inventory adjustment and event publishing
func TestE2E_SaleCaptureAndInventoryAdjustment(t *testing.T) {
	store, handler, pub, shopID, deviceToken := setupE2EFixture(t)

	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_e2e_sale_1",
		"transcript": "Two Milo, four thousand naira. Thank you."
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for sale capture, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		CaptureID      string               `json:"capture_id"`
		Transactions   []models.Transaction `json:"transactions"`
		RequiresReview bool                 `json:"requires_review"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if res.RequiresReview {
		t.Fatalf("expected clean capture not to require review")
	}
	if len(res.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(res.Transactions))
	}
	if res.Transactions[0].Status != models.StatusConfirmed {
		t.Fatalf("expected confirmed status, got %s", res.Transactions[0].Status)
	}

	// Verify Milo stock decreased from 20 to 18
	milo, err := store.GetProductByID(context.Background(), shopID, "prd_milo")
	if err != nil {
		t.Fatalf("GetProductByID: %v", err)
	}
	if milo.Quantity != 18 {
		t.Fatalf("expected Milo stock 18, got %d", milo.Quantity)
	}

	// Verify events were published
	if pub.countEvents(ws.EventTransactionCreated) != 1 {
		t.Fatalf("expected 1 transaction.created event, got %d", pub.countEvents(ws.EventTransactionCreated))
	}
	if pub.countEvents(ws.EventInventoryUpdated) != 1 {
		t.Fatalf("expected 1 inventory.updated event, got %d", pub.countEvents(ws.EventInventoryUpdated))
	}
}

// 2. Held review-required capture: zero inventory mutation until confirmed
func TestE2E_HeldReviewRequiredCapture(t *testing.T) {
	store, handler, pub, shopID, deviceToken := setupE2EFixture(t)

	// Unrecognized product
	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_e2e_held_1",
		"transcript": "Two Unknown Biscuits, five hundred naira. Thank you."
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Transactions   []models.Transaction `json:"transactions"`
		RequiresReview bool                 `json:"requires_review"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.RequiresReview {
		t.Fatalf("expected capture to require review")
	}
	heldTxn := res.Transactions[0]
	if heldTxn.Status != models.StatusReviewRequired {
		t.Fatalf("expected review_required status, got %s", heldTxn.Status)
	}

	// Verify ZERO stock changes occurred across all catalogue products
	milo, _ := store.GetProductByID(context.Background(), shopID, "prd_milo")
	pepsi, _ := store.GetProductByID(context.Background(), shopID, "prd_pepsi")
	if milo.Quantity != 20 || pepsi.Quantity != 8 {
		t.Fatalf("stock modified for review_required transaction: milo=%d, pepsi=%d", milo.Quantity, pepsi.Quantity)
	}

	// Verify transaction.review_required event fired, NOT transaction.created or inventory.updated
	if pub.countEvents(ws.EventTransactionReviewRequired) != 1 {
		t.Fatalf("expected 1 review_required event, got %d", pub.countEvents(ws.EventTransactionReviewRequired))
	}
	if pub.countEvents(ws.EventInventoryUpdated) != 0 {
		t.Fatalf("expected 0 inventory.updated events, got %d", pub.countEvents(ws.EventInventoryUpdated))
	}

	// Now resolve review via Edit & Confirm (mapping to Milo)
	corrBody := bytes.NewBufferString(`{
		"product_id": "prd_milo",
		"quantity": 2,
		"unit": "pack",
		"notes": "Spoken biscuits meant Milo"
	}`)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/transactions/%s/confirm", heldTxn.ID), corrBody)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("confirm review failed: %d: %s", rec.Code, rec.Body.String())
	}

	// Milo stock should now be 18
	milo, _ = store.GetProductByID(context.Background(), shopID, "prd_milo")
	if milo.Quantity != 18 {
		t.Fatalf("expected Milo stock 18 after confirmation, got %d", milo.Quantity)
	}
}

// 3. Multi-line restock receipt: atomic stock updates across multiple products
func TestE2E_MultiLineRestockReceiptAtomicity(t *testing.T) {
	store, handler, _, shopID, deviceToken := setupE2EFixture(t)

	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_e2e_restock_1",
		"transcript": "Five packs of Pepsi, twelve thousand naira. Three cartons of Milo, forty-five thousand naira. Stock received."
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for restock, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Transactions   []models.Transaction `json:"transactions"`
		RequiresReview bool                 `json:"requires_review"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.RequiresReview {
		t.Fatalf("expected clean restock not to require review")
	}
	if len(res.Transactions) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(res.Transactions))
	}

	// Pepsi: 8 + 5 = 13
	pepsi, _ := store.GetProductByID(context.Background(), shopID, "prd_pepsi")
	if pepsi.Quantity != 13 {
		t.Fatalf("expected Pepsi stock 13, got %d", pepsi.Quantity)
	}

	// Milo: 20 + 3 cartons (x12) = 20 + 36 = 56
	milo, _ := store.GetProductByID(context.Background(), shopID, "prd_milo")
	if milo.Quantity != 56 {
		t.Fatalf("expected Milo stock 56, got %d", milo.Quantity)
	}
}

// 4. Multi-line receipt atomicity rollback when line 2 fails (e.g. insufficient stock)
func TestE2E_MultiLineReceiptAtomicityRollbackOnLineFailure(t *testing.T) {
	store, handler, pub, shopID, deviceToken := setupE2EFixture(t)

	// Line 1: valid restock of 2 cartons of Milo (+24)
	// Line 2: sale of 999 packs of Pepsi (insufficient stock, exceeds 8)
	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_e2e_rollback_1",
		"type": "sale",
		"extracted_lines": [
			{
				"product_name": "Milo 500g",
				"quantity": 2,
				"unit": "pack",
				"amount": {"amount": 400000, "currency": "NGN"}
			},
			{
				"product_name": "Pepsi 50cl",
				"quantity": 999,
				"unit": "pack",
				"amount": {"amount": 239760000, "currency": "NGN"}
			}
		],
		"transcript": "Two packs of Milo and nine hundred and ninety nine packs of Pepsi. Thank you."
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Entire receipt must be rejected with 409 Conflict
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on insufficient stock line failure, got %d: %s", rec.Code, rec.Body.String())
	}

	// Milo stock MUST REMAIN AT 20 (Line 1 rolled back completely!)
	milo, _ := store.GetProductByID(context.Background(), shopID, "prd_milo")
	if milo.Quantity != 20 {
		t.Fatalf("CRITICAL ATOMICITY VIOLATION: Milo stock was mutated to %d despite line 2 failure!", milo.Quantity)
	}

	// Pepsi stock MUST REMAIN AT 8
	pepsi, _ := store.GetProductByID(context.Background(), shopID, "prd_pepsi")
	if pepsi.Quantity != 8 {
		t.Fatalf("Pepsi stock mutated to %d!", pepsi.Quantity)
	}

	// Zero events must have been published
	if pub.countEvents(ws.EventTransactionCreated) != 0 {
		t.Fatalf("events published for rolled back receipt: %d", pub.countEvents(ws.EventTransactionCreated))
	}
	if pub.countEvents(ws.EventInventoryUpdated) != 0 {
		t.Fatalf("inventory events published for rolled back receipt: %d", pub.countEvents(ws.EventInventoryUpdated))
	}
}

// 5. Deterministic idempotency replay against catalogue drift and duplicate protection
func TestE2E_IdempotencyReplayAndCatalogueDrift(t *testing.T) {
	store, handler, _, shopID, deviceToken := setupE2EFixture(t)

	captureBody := `{
		"capture_id": "cap_e2e_idem_1",
		"transcript": "Two Milo, four thousand naira. Thank you."
	}`

	// First execution -> 201 Created
	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(captureBody))
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("initial capture failed: %d: %s", rec.Code, rec.Body.String())
	}

	// Stock is 20 - 2 = 18
	milo, _ := store.GetProductByID(context.Background(), shopID, "prd_milo")
	if milo.Quantity != 18 {
		t.Fatalf("expected Milo stock 18, got %d", milo.Quantity)
	}

	// Simulate catalogue drift: update Milo price in catalogue from 200000 to 250000
	milo.Price.Amount = 250000
	if _, err := store.UpdateProduct(context.Background(), milo); err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}

	// Replay exact same capture payload -> MUST RETURN 200 OK (replayed)
	req = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(captureBody))
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("replayed capture expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	// Stock MUST STILL BE 18 (no double deduction)
	milo, _ = store.GetProductByID(context.Background(), shopID, "prd_milo")
	if milo.Quantity != 18 {
		t.Fatalf("expected Milo stock 18 after replay, got %d", milo.Quantity)
	}

	// Replay with DIFFERENT content for same capture ID -> MUST RETURN 409 Conflict
	conflictBody := `{
		"capture_id": "cap_e2e_idem_1",
		"transcript": "Three Milo, six thousand naira. Thank you."
	}`
	req = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(conflictBody))
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("conflicting capture payload expected 409 Conflict, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 6. Cross-shop isolation and device revocation guards
func TestE2E_CrossShopIsolationAndRevocation(t *testing.T) {
	store, handler, _, _, deviceToken := setupE2EFixture(t)

	// Create second shop
	shop2Account := storage.Account{
		Identity: storage.Identity{
			OwnerID:  "own_shop_2",
			Email:    "shop2@example.com",
			ShopID:   "shop_e2e_2",
			ShopName: "Shop Two",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "pass",
		RecoveryHash: "rec",
	}
	if err := store.CreateAccount(context.Background(), shop2Account); err != nil {
		t.Fatalf("CreateAccount shop2: %v", err)
	}

	// Pair device in Shop 2
	pairBody := bytes.NewBufferString(`{"name":"Shop 2 Counter Mic"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices", pairBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	// Handler resolving shop_e2e_2
	resolverShop2 := func(r *http.Request) (string, error) {
		return "shop_e2e_2", nil
	}
	handlerShop2 := api.New(store, nil, resolverShop2, nil)
	handlerShop2.ServeHTTP(rec, req)

	var pairResShop2 struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pairResShop2)

	// Device 1 submits capture: token belongs to shop 1, but device masquerades as device from shop 2
	masqueradeBody := fmt.Sprintf(`{
		"capture_id": "cap_cross_1",
		"device_id": "%s",
		"transcript": "Two Milo. Thank you."
	}`, pairResShop2.ID)
	req = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(masqueradeBody))
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for mismatched device ID, got %d: %s", rec.Code, rec.Body.String())
	}

	// Revoke Device in Shop 2
	req = httptest.NewRequest(http.MethodPost, "/api/devices/"+pairResShop2.ID+"/revoke", nil)
	rec = httptest.NewRecorder()
	handlerShop2.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke failed: %d", rec.Code)
	}

	// Subsequent capture with revoked device token -> 401 Unauthorized
	req = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(`{"transcript":"Two Milo. Thank you."}`))
	req.Header.Set("Authorization", "Bearer "+pairResShop2.Token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handlerShop2.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for revoked device, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 7. WebSocket reconnect recovery: snapshot query consistency after capture events
func TestE2E_WebSocketReconnectRecovery(t *testing.T) {
	_, handler, _, _, deviceToken := setupE2EFixture(t)

	// Record a sale capture
	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_e2e_reconn_1",
		"transcript": "Two Milo, four thousand naira. Thank you."
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("capture failed: %d: %s", rec.Code, rec.Body.String())
	}

	// Client reconnects: queries GET /api/transactions
	req = httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot fetch failed: %d", rec.Code)
	}
	var txnSnapshot struct {
		Transactions []models.Transaction `json:"transactions"`
		Total        int                  `json:"total"`
		GeneratedAt  string               `json:"generated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &txnSnapshot); err != nil {
		t.Fatalf("decode transactions snapshot: %v", err)
	}
	if txnSnapshot.Total != 1 || len(txnSnapshot.Transactions) != 1 {
		t.Fatalf("expected 1 transaction in snapshot, got %d", txnSnapshot.Total)
	}
	if txnSnapshot.GeneratedAt == "" {
		t.Fatalf("expected generated_at timestamp on snapshot")
	}

	// Queries GET /api/inventory
	req = httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("inventory snapshot fetch failed: %d", rec.Code)
	}
	var invSnapshot struct {
		Products    []models.Product `json:"products"`
		GeneratedAt string           `json:"generated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &invSnapshot); err != nil {
		t.Fatalf("decode inventory snapshot: %v", err)
	}
	var miloFound *models.Product
	for _, p := range invSnapshot.Products {
		if p.ID == "prd_milo" {
			miloFound = &p
			break
		}
	}
	if miloFound == nil || miloFound.Quantity != 18 {
		t.Fatalf("expected Milo stock 18 in inventory snapshot, got %+v", miloFound)
	}
}
