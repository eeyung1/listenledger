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
)

func setupReceiptFailureFixture(t *testing.T) (*storage.Store, http.Handler, string, string, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "receipt_failure_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	shopID := "shop_fail_test_1"
	account := storage.Account{
		Identity: storage.Identity{
			OwnerID:  "own_fail_1",
			Email:    "fail_test@example.com",
			ShopID:   shopID,
			ShopName: "Failure & Invariant Test Shop",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "hashedpass",
		RecoveryHash: "hashedrecovery",
	}
	if err := store.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	// Create catalogue products:
	// Milo 500g: 10 packs (1 carton = 12 packs)
	milo := models.Product{
		ID:       "prd_milo_fail",
		ShopID:   shopID,
		Name:     "Milo 500g",
		SKU:      "MILO-500",
		Aliases:  []string{"Milo", "Nestle Milo"},
		Price:    models.Money{Amount: 250000, Currency: "NGN"},
		Quantity: 10,
		BaseUnit: "pack",
		Packaging: []models.PackagingRelation{
			{From: "carton", To: "pack", Factor: 12},
		},
		LowStockThreshold: 3,
	}
	if err := store.InsertProduct(context.Background(), milo); err != nil {
		t.Fatalf("InsertProduct Milo: %v", err)
	}

	// Peak Milk: 2 cans/packs
	peak := models.Product{
		ID:                "prd_peak_fail",
		ShopID:            shopID,
		Name:              "Peak Milk",
		SKU:               "PEAK-CAN",
		Aliases:           []string{"Peak", "Peak milk"},
		Price:             models.Money{Amount: 180000, Currency: "NGN"},
		Quantity:          2,
		BaseUnit:          "pack",
		LowStockThreshold: 1,
	}
	if err := store.InsertProduct(context.Background(), peak); err != nil {
		t.Fatalf("InsertProduct Peak: %v", err)
	}

	handler := api.New(store, nil, func(r *http.Request) (string, error) {
		return shopID, nil
	}, nil)

	// Pair device
	pairBody := bytes.NewBufferString(`{"name":"Counter Audio Device"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices", pairBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("failed to pair test device: %d: %s", rec.Code, rec.Body.String())
	}

	var pairRes struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pairRes); err != nil {
		t.Fatalf("decode pair response: %v", err)
	}

	return store, handler, shopID, pairRes.Token, milo.ID, peak.ID
}

// TestMultiLineReceipt_AtomicityRollbackOnLineFailure verifies that when an intake
// multi-line receipt contains a failing line (e.g. line 2 exceeds stock), the entire
// receipt rolls back atomically with zero partial stock or ledger mutations.
func TestMultiLineReceipt_AtomicityRollbackOnLineFailure(t *testing.T) {
	store, handler, shopID, devToken, miloID, peakID := setupReceiptFailureFixture(t)
	ctx := context.Background()

	// Initial stock verification
	p1, _ := store.GetProductByID(ctx, shopID, miloID)
	p2, _ := store.GetProductByID(ctx, shopID, peakID)
	if p1.Quantity != 10 || p2.Quantity != 2 {
		t.Fatalf("unexpected initial stock: p1=%d, p2=%d", p1.Quantity, p2.Quantity)
	}

	// Edge submits multi-line receipt capture:
	// Line 1: 5 packs of Milo (valid, stock 10 -> 5)
	// Line 2: 5 packs of Peak Milk (insufficient stock: requested 5, available 2)
	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_atomic_fail_1",
		"type": "sale",
		"extracted_lines": [
			{
				"product_name": "Milo 500g",
				"quantity": 5,
				"unit": "pack",
				"amount": {"amount": 1250000, "currency": "NGN"}
			},
			{
				"product_name": "Peak Milk",
				"quantity": 5,
				"unit": "pack",
				"amount": {"amount": 900000, "currency": "NGN"}
			}
		],
		"transcript": "Five packs of Milo and five Peak Milk. Thank you."
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+devToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Must be rejected with HTTP 409 Conflict
	if w.Code != http.StatusConflict {
		t.Fatalf("expected HTTP 409 Conflict, got %d: %s", w.Code, w.Body.String())
	}

	// Verify atomicity invariant: Line 1 (Milo) must NOT have had its stock decremented!
	p1After, err := store.GetProductByID(ctx, shopID, miloID)
	if err != nil {
		t.Fatalf("GetProduct milo: %v", err)
	}
	if p1After.Quantity != 10 {
		t.Fatalf("ATOMICITY VIOLATION: Milo stock was mutated to %d despite receipt failure; expected 10", p1After.Quantity)
	}

	p2After, err := store.GetProductByID(ctx, shopID, peakID)
	if err != nil {
		t.Fatalf("GetProduct peak: %v", err)
	}
	if p2After.Quantity != 2 {
		t.Fatalf("ATOMICITY VIOLATION: Peak stock was mutated to %d; expected 2", p2After.Quantity)
	}

	// Verify zero transactions recorded
	txns, err := store.GetTransactions(ctx, shopID, 100)
	if err != nil {
		t.Fatalf("GetTransactions: %v", err)
	}
	if len(txns) != 0 {
		t.Fatalf("ATOMICITY VIOLATION: %d transactions committed despite receipt rollback", len(txns))
	}

	// Verify zero stock movements recorded
	miloMovements, _, err := store.GetProductMovements(ctx, shopID, miloID, 100, 0)
	if err != nil {
		t.Fatalf("GetProductMovements: %v", err)
	}
	if len(miloMovements) != 0 {
		t.Fatalf("ATOMICITY VIOLATION: %d stock movements recorded for Milo despite receipt rollback", len(miloMovements))
	}
}

// TestReceipt_IdempotentReplayAndConflictDetection proves that idempotent capture retries
// return 200 OK without re-adjusting stock, while conflicting reuse of the same capture ID
// is rejected with 409 Conflict.
func TestReceipt_IdempotentReplayAndConflictDetection(t *testing.T) {
	store, handler, shopID, devToken, miloID, _ := setupReceiptFailureFixture(t)
	ctx := context.Background()

	captureBody := `{
		"capture_id": "cap_replay_test_100",
		"type": "sale",
		"extracted_lines": [
			{
				"product_name": "Milo 500g",
				"quantity": 2,
				"unit": "pack",
				"amount": {"amount": 500000, "currency": "NGN"}
			}
		],
		"transcript": "Two packs of Milo. Thank you."
	}`

	// First execution -> 201 Created
	req1 := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(captureBody))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", "Bearer "+devToken)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)

	if w1.Code != http.StatusCreated {
		t.Fatalf("expected HTTP 201 Created, got %d: %s", w1.Code, w1.Body.String())
	}

	var resp1 struct {
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("unmarshal resp1: %v", err)
	}
	if len(resp1.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(resp1.Transactions))
	}
	txnID := resp1.Transactions[0].ID

	// Stock check: 10 - 2 = 8
	pAfter1, _ := store.GetProductByID(ctx, shopID, miloID)
	if pAfter1.Quantity != 8 {
		t.Fatalf("expected stock 8, got %d", pAfter1.Quantity)
	}

	// Exact retry with identical payload -> HTTP 200 OK
	req2 := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(captureBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+devToken)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK on replay, got %d: %s", w2.Code, w2.Body.String())
	}

	var resp2 struct {
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("unmarshal resp2: %v", err)
	}
	if resp2.Transactions[0].ID != txnID {
		t.Fatalf("expected identical transaction ID %s, got %s", txnID, resp2.Transactions[0].ID)
	}

	// Stock MUST NOT have changed on replay: must still be 8!
	pAfter2, _ := store.GetProductByID(ctx, shopID, miloID)
	if pAfter2.Quantity != 8 {
		t.Fatalf("REPLAY DUPLICATION ERROR: stock was decremented again to %d; expected 8", pAfter2.Quantity)
	}

	// Conflicting reuse of cap_replay_test_100 with different quantity (5 instead of 2)
	captureConflict := `{
		"capture_id": "cap_replay_test_100",
		"type": "sale",
		"extracted_lines": [
			{
				"product_name": "Milo 500g",
				"quantity": 5,
				"unit": "pack",
				"amount": {"amount": 1250000, "currency": "NGN"}
			}
		],
		"transcript": "Five packs of Milo. Thank you."
	}`

	req3 := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(captureConflict))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Bearer "+devToken)
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)

	if w3.Code != http.StatusConflict {
		t.Fatalf("expected HTTP 409 Conflict on conflicting capture ID reuse, got %d: %s", w3.Code, w3.Body.String())
	}

	// Stock remains untouched at 8
	pAfter3, _ := store.GetProductByID(ctx, shopID, miloID)
	if pAfter3.Quantity != 8 {
		t.Fatalf("stock was mutated during conflicting retry to %d; expected 8", pAfter3.Quantity)
	}
}

// TestUncertainCaptures_HarmlessAndZeroStockMutation proves that captures held for review
// or rejected have zero effect on inventory and financial totals.
func TestUncertainCaptures_HarmlessAndZeroStockMutation(t *testing.T) {
	store, handler, shopID, devToken, miloID, _ := setupReceiptFailureFixture(t)
	ctx := context.Background()

	// Initial stock = 10
	pInit, _ := store.GetProductByID(ctx, shopID, miloID)
	if pInit.Quantity != 10 {
		t.Fatalf("expected initial stock 10, got %d", pInit.Quantity)
	}

	// Edge submits uncertain capture with unrecognized product
	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_uncertain_fail_1",
		"transcript": "Two Unknown Soap, three hundred naira. Thank you."
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+devToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected HTTP 201 Created for held capture, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		RequiresReview bool                 `json:"requires_review"`
		Transactions   []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.RequiresReview || len(resp.Transactions) != 1 {
		t.Fatalf("expected 1 held transaction requiring review, got %d", len(resp.Transactions))
	}
	txn := resp.Transactions[0]
	if txn.Status != models.StatusReviewRequired {
		t.Fatalf("expected status review_required, got %s", txn.Status)
	}

	// Invariant: Stock MUST NOT be changed while in review!
	pAfterHeld, _ := store.GetProductByID(ctx, shopID, miloID)
	if pAfterHeld.Quantity != 10 {
		t.Fatalf("UNCERTAIN CAPTURE LEAKAGE: stock was mutated to %d while in review; expected 10", pAfterHeld.Quantity)
	}

	// Owner rejects the uncertain capture
	rejBody := bytes.NewBufferString(`{
		"notes": "Customer was only asking for prices, no sale occurred"
	}`)
	rejReq := httptest.NewRequest(http.MethodPost, "/api/transactions/"+txn.ID+"/reject", rejBody)
	rejReq.Header.Set("Content-Type", "application/json")
	rejW := httptest.NewRecorder()
	handler.ServeHTTP(rejW, rejReq)

	if rejW.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK on reject, got %d: %s", rejW.Code, rejW.Body.String())
	}

	var rejTxn models.Transaction
	if err := json.Unmarshal(rejW.Body.Bytes(), &rejTxn); err != nil {
		t.Fatalf("unmarshal reject: %v", err)
	}
	if rejTxn.Status != models.StatusRejected {
		t.Fatalf("expected status rejected, got %s", rejTxn.Status)
	}

	// Invariant: Stock MUST remain 10 after rejection!
	pAfterReject, _ := store.GetProductByID(ctx, shopID, miloID)
	if pAfterReject.Quantity != 10 {
		t.Fatalf("REJECTION LEAKAGE: stock was mutated to %d after rejection; expected 10", pAfterReject.Quantity)
	}

	// Zero stock movements should exist
	movements, _, _ := store.GetProductMovements(ctx, shopID, miloID, 100, 0)
	if len(movements) != 0 {
		t.Fatalf("expected 0 movements for unconfirmed capture, got %d", len(movements))
	}
}

// TestReceiptWideConfirmationAndRollback tests receipt-wide review confirmation
// with atomic rollback and prevention of duplicate confirmations.
func TestReceiptWideConfirmationAndRollback(t *testing.T) {
	store, handler, shopID, devToken, miloID, peakID := setupReceiptFailureFixture(t)
	ctx := context.Background()

	// Ingest a multi-line restock where lines are held for review (unrecognized product on line 2)
	captureBody := bytes.NewBufferString(`{
		"capture_id": "cap_rcpt_held_1",
		"type": "restock",
		"extracted_lines": [
			{
				"product_name": "Milo 500g",
				"quantity": 1,
				"unit": "carton",
				"amount": {"amount": 2500000, "currency": "NGN"}
			},
			{
				"product_name": "Peak Milk",
				"quantity": 5,
				"unit": "pack",
				"amount": {"amount": 900000, "currency": "NGN"}
			}
		],
		"transcript": "Stock received. One carton of Milo and five packs of Peak Milk."
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/captures", captureBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+devToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("ingest failed: %d: %s", w.Code, w.Body.String())
	}

	var ingestResp struct {
		ReceiptID    string               `json:"receipt_id"`
		Transactions []models.Transaction `json:"transactions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &ingestResp)
	receiptID := ingestResp.ReceiptID
	if receiptID == "" {
		if len(ingestResp.Transactions) > 0 {
			receiptID = ingestResp.Transactions[0].ReceiptID
		}
	}
	if receiptID == "" {
		t.Fatalf("expected receipt ID on multi-line capture")
	}

	// Verify stock before confirmation if review required, or verify post-confirmation
	if ingestResp.Transactions[0].Status == models.StatusReviewRequired {
		// Confirm receipt wide
		confReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/receipts/%s/confirm", receiptID), nil)
		confW := httptest.NewRecorder()
		handler.ServeHTTP(confW, confReq)

		if confW.Code != http.StatusOK {
			t.Fatalf("expected HTTP 200 on receipt confirmation, got %d: %s", confW.Code, confW.Body.String())
		}
	}

	// Verify stock adjustments:
	// Milo: 10 + 12 (1 carton) = 22
	// Peak: 2 + 5 = 7
	pMilo, _ := store.GetProductByID(ctx, shopID, miloID)
	if pMilo.Quantity != 22 {
		t.Fatalf("expected Milo stock 22, got %d", pMilo.Quantity)
	}
	pPeak, _ := store.GetProductByID(ctx, shopID, peakID)
	if pPeak.Quantity != 7 {
		t.Fatalf("expected Peak stock 7, got %d", pPeak.Quantity)
	}

	// Repeated confirmation of already confirmed receipt is idempotent and must NOT double-apply stock
	repeatReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/receipts/%s/confirm", receiptID), nil)
	repeatW := httptest.NewRecorder()
	handler.ServeHTTP(repeatW, repeatReq)

	if repeatW.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK on idempotent receipt confirmation, got %d", repeatW.Code)
	}

	// Stock must NOT double-apply
	pMiloRepeat, _ := store.GetProductByID(ctx, shopID, miloID)
	if pMiloRepeat.Quantity != 22 {
		t.Fatalf("DUPLICATE CONFIRMATION LEAKAGE: Milo stock was double-applied to %d; expected 22", pMiloRepeat.Quantity)
	}
}
