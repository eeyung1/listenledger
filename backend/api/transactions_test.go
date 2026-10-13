package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

func TestConfidenceCannotBypassReview(t *testing.T) {
	high, low := 0.99, 0.2
	for name, c := range map[string]*models.Confidence{"missing": nil, "partial": {Overall: high, Product: &high}, "weak": {Overall: high, Product: &low, Quantity: &high, Amount: &high}, "zero overall": {Overall: 0, Product: &high, Quantity: &high, Amount: &high}} {
		if deriveStatus(c) != models.StatusReviewRequired {
			t.Fatal(name)
		}
	}
	if deriveStatus(&models.Confidence{Overall: high, Product: &high, Quantity: &high, Amount: &high}) != models.StatusConfirmed {
		t.Fatal("strong anchors held")
	}
	b := createTransactionRequest{Type: models.TypeSale, ProductName: "Milo", Quantity: 1, Unit: models.UnitPack, Amount: models.Money{Currency: "NGN", Amount: 100}, Status: models.StatusConfirmed}
	if _, e := b.transaction("a"); e == nil {
		t.Fatal("caller forced confirmation")
	}
}

type testPublisher struct {
	events []ws.EventType
}

func (p *testPublisher) Publish(_ string, eventType ws.EventType, _ any) bool {
	p.events = append(p.events, eventType)
	return true
}

func testServerFixture(t *testing.T) (http.Handler, *storage.Store, *testPublisher, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "api_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	shopID := "shop_1"
	err = store.CreateAccount(ctx, storage.Account{
		Identity: storage.Identity{
			OwnerID:  "owner_1",
			Email:    "test@example.com",
			ShopID:   shopID,
			ShopName: "Test Shop",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "pass",
		RecoveryHash: "rec",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = store.InsertProduct(ctx, models.Product{
		ID:       "prd_milo",
		ShopID:   shopID,
		Name:     "Milo",
		SKU:      "MILO-1",
		Price:    models.Money{Amount: 200000, Currency: "NGN"},
		Quantity: 10,
		BaseUnit: models.UnitPack,
	})
	if err != nil {
		t.Fatal(err)
	}

	pub := &testPublisher{}
	handler := New(store, pub, auth.QueryShopResolver, nil)
	return handler, store, pub, shopID
}

func TestReviewConfirmAndRejectEndpoints(t *testing.T) {
	handler, store, pub, shopID := testServerFixture(t)
	ctx := context.Background()

	// 1. Insert a review_required transaction directly via store.RecordMovement
	heldTx := models.Transaction{
		ID:          "txn_review_1",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo",
		Quantity:    3,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 600000, Currency: "NGN"},
		Status:      models.StatusReviewRequired,
		OccurredAt:  time.Now(),
		CreatedAt:   time.Now(),
	}
	_, err := store.RecordMovement(ctx, heldTx, "req_1", "hash_1")
	if err != nil {
		t.Fatal(err)
	}

	// 2. Confirm the transaction via POST /api/transactions/txn_review_1/confirm
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/txn_review_1/confirm?shop_id="+shopID, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on confirm, got %d: %s", w.Code, w.Body.String())
	}

	var confirmed models.Transaction
	if err := json.Unmarshal(w.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != models.StatusConfirmed {
		t.Fatalf("expected status confirmed, got %s", confirmed.Status)
	}

	// Verify product stock was reduced
	prod, err := store.GetProductByID(ctx, shopID, "prd_milo")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Quantity != 7 { // 10 - 3 = 7
		t.Fatalf("expected stock 7, got %d", prod.Quantity)
	}

	// Verify events published: EventInventoryUpdated and EventTransactionUpdated
	if len(pub.events) < 2 {
		t.Fatalf("expected at least 2 events published, got %d", len(pub.events))
	}

	// 3. Duplicate confirm should return 409 Conflict
	wDup := httptest.NewRecorder()
	reqDup := httptest.NewRequest(http.MethodPost, "/api/transactions/txn_review_1/confirm?shop_id="+shopID, nil)
	handler.ServeHTTP(wDup, reqDup)
	if wDup.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on duplicate confirm, got %d", wDup.Code)
	}

	// 4. Create another review transaction to test reject
	heldReject := models.Transaction{
		ID:          "txn_review_2",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo",
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 400000, Currency: "NGN"},
		Status:      models.StatusReviewRequired,
		OccurredAt:  time.Now(),
		CreatedAt:   time.Now(),
	}
	_, err = store.RecordMovement(ctx, heldReject, "req_2", "hash_2")
	if err != nil {
		t.Fatal(err)
	}

	// Reject it
	wRej := httptest.NewRecorder()
	reqRej := httptest.NewRequest(http.MethodPost, "/api/transactions/txn_review_2/reject?shop_id="+shopID, nil)
	handler.ServeHTTP(wRej, reqRej)
	if wRej.Code != http.StatusOK {
		t.Fatalf("expected 200 on reject, got %d: %s", wRej.Code, wRej.Body.String())
	}

	prodAfterReject, err := store.GetProductByID(ctx, shopID, "prd_milo")
	if err != nil {
		t.Fatal(err)
	}
	if prodAfterReject.Quantity != 7 { // stock untouched
		t.Fatalf("expected stock 7 untouched, got %d", prodAfterReject.Quantity)
	}
}

func TestProductDetailAndPatchEndpoints(t *testing.T) {
	handler, store, _, shopID := testServerFixture(t)
	ctx := context.Background()

	// GET /api/products/prd_milo
	wGet := httptest.NewRecorder()
	reqGet := httptest.NewRequest(http.MethodGet, "/api/products/prd_milo?shop_id="+shopID, nil)
	handler.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 on get product, got %d", wGet.Code)
	}

	// PATCH /api/products/prd_milo to change name and price
	newName := "Milo 500g Refill"
	newPrice := models.Money{Amount: 250000, Currency: "NGN"}
	aliases := []string{"Milo Refill", "Big Milo"}
	patchBody, _ := json.Marshal(map[string]any{
		"name":    newName,
		"price":   newPrice,
		"aliases": aliases,
	})

	wPatch := httptest.NewRecorder()
	reqPatch := httptest.NewRequest(http.MethodPatch, "/api/products/prd_milo?shop_id="+shopID, bytes.NewReader(patchBody))
	handler.ServeHTTP(wPatch, reqPatch)
	if wPatch.Code != http.StatusOK {
		t.Fatalf("expected 200 on patch product, got %d: %s", wPatch.Code, wPatch.Body.String())
	}

	updated, err := store.GetProductByID(ctx, shopID, "prd_milo")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != newName || updated.Price.Amount != 250000 || len(updated.Aliases) != 2 {
		t.Fatalf("unexpected updated product: %+v", updated)
	}
}
