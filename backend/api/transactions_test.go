package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestLedgerPayloadsOmitLegacyAudio(t *testing.T) {
	handler, store, _, shopID := testServerFixture(t)
	ctx := context.Background()

	const snippet = "data:audio/webm;base64,GkXfo59ChoEBQveBAULygQ8="
	legacy := models.Transaction{
		ID:          "txn_audio_legacy",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "prd_milo",
		ProductName: "Milo",
		Quantity:    1,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 200000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now(),
		CreatedAt:   time.Now(),
		AudioData:   snippet,
	}
	if err := store.InsertTransaction(ctx, legacy); err != nil {
		t.Fatal(err)
	}

	// Ordinary ledger snapshots must advertise the snippet without shipping it.
	listReq := httptest.NewRequest(http.MethodGet, "/api/transactions?shop_id="+shopID, nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "audio_data") {
		t.Fatalf("ledger list leaked audio_data: %s", listRec.Body.String())
	}
	var snapshot transactionsResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, txn := range snapshot.Transactions {
		if txn.ID == legacy.ID {
			found = true
			if !txn.HasAudio {
				t.Error("legacy transaction must report has_audio in the list")
			}
			if txn.AudioData != "" {
				t.Errorf("list transaction retained raw audio: %q", txn.AudioData)
			}
		}
	}
	if !found {
		t.Fatal("legacy transaction missing from ledger list")
	}

	// WebSocket transaction events marshal the same model; they must not carry
	// the snippet either.
	wire, err := json.Marshal(models.Transaction{AudioData: snippet, HasAudio: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "audio_data") {
		t.Fatalf("transaction event payload leaked audio_data: %s", wire)
	}
	if !strings.Contains(string(wire), `"has_audio":true`) {
		t.Fatalf("transaction event payload lost has_audio: %s", wire)
	}

	// The shop-scoped detail endpoint remains the explicit playback path.
	detailReq := httptest.NewRequest(http.MethodGet, "/api/transactions/"+legacy.ID+"?shop_id="+shopID, nil)
	detailRec := httptest.NewRecorder()
	handler.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", detailRec.Code, detailRec.Body.String())
	}
	var detail struct {
		AudioData string `json:"audio_data"`
		HasAudio  bool   `json:"has_audio"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.AudioData != snippet {
		t.Errorf("detail audio_data = %q, want snippet", detail.AudioData)
	}
	if !detail.HasAudio {
		t.Error("detail must report has_audio for a legacy snippet")
	}
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

func TestConfirmTransaction_SaveAsAlias(t *testing.T) {
	ctx := context.Background()
	handler, store, pub, shopID := testServerFixture(t)

	prod := models.Product{
		ID:       "prd_peak",
		ShopID:   shopID,
		Name:     "Peak Milk 400g",
		SKU:      "PEAK-400",
		Aliases:  []string{"Peak"},
		Price:    models.Money{Amount: 350000, Currency: "NGN"},
		Quantity: 20,
		BaseUnit: models.UnitTin,
	}
	if err := store.InsertProduct(ctx, prod); err != nil {
		t.Fatal(err)
	}

	// Held review transaction with misheard product name "peek"
	heldTx := models.Transaction{
		ID:          "txn_review_peek",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   "",
		ProductName: "peek",
		Quantity:    2,
		Unit:        models.UnitTin,
		Amount:      models.Money{Amount: 700000, Currency: "NGN"},
		Status:      models.StatusReviewRequired,
		OccurredAt:  time.Now(),
		CreatedAt:   time.Now(),
	}
	if _, err := store.RecordMovement(ctx, heldTx, "req_alias_1", "hash_alias_1"); err != nil {
		t.Fatal(err)
	}

	// Confirm with save_as_alias: true (omitting alias_text to test auto-derivation from origTxn.ProductName)
	confirmBody, _ := json.Marshal(map[string]any{
		"product_id":    "prd_peak",
		"quantity":      2,
		"unit":          models.UnitTin,
		"amount":        models.Money{Amount: 700000, Currency: "NGN"},
		"save_as_alias": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/txn_review_peek/confirm?shop_id="+shopID, bytes.NewReader(confirmBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on confirm, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the product now has "peek" in its aliases
	updatedProd, err := store.GetProductByID(ctx, shopID, "prd_peak")
	if err != nil {
		t.Fatal(err)
	}
	hasPeek := false
	for _, a := range updatedProd.Aliases {
		if a == "peek" {
			hasPeek = true
			break
		}
	}
	if !hasPeek {
		t.Fatalf("expected 'peek' in aliases, got %v", updatedProd.Aliases)
	}

	// Verify EventInventoryUpdated was published
	hasInvUpdated := false
	for _, ev := range pub.events {
		if ev == ws.EventInventoryUpdated {
			hasInvUpdated = true
			break
		}
	}
	if !hasInvUpdated {
		t.Errorf("expected EventInventoryUpdated event published, got %v", pub.events)
	}
}
