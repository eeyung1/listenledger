package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/validate"
)

func setupTestApp(t *testing.T) (*storage.Store, http.Handler, string) {
	t.Helper()
	store, err := storage.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	testID := time.Now().Format("20060102150405.000000000")
	shopID := "shop_" + testID
	// Create owner and shop
	err = store.CreateAccount(context.Background(), storage.Account{
		Identity: storage.Identity{
			OwnerID:  "owner_" + testID,
			Email:    "owner_" + testID + "@example.com",
			ShopID:   shopID,
			ShopName: "Test Shop M7",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "dummyhash",
		RecoveryHash: "dummyrecovery",
	})
	if err != nil {
		t.Fatalf("failed to create test account: %v", err)
	}

	handler := api.New(store, nil, func(r *http.Request) (string, error) {
		return shopID, nil
	}, nil)

	return store, handler, shopID
}

func TestCanonicalDeviceIdentityEnforcement(t *testing.T) {
	store, handler, shopID := setupTestApp(t)
	ctx := context.Background()

	// Pair device 1
	pairReq := bytes.NewBufferString(`{"name":"Counter Mic"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices", pairReq)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("failed to pair device: %d, body: %s", w.Code, w.Body.String())
	}

	var pairedDev struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pairedDev); err != nil {
		t.Fatal(err)
	}

	// Pair device 2
	pairReq2 := bytes.NewBufferString(`{"name":"Backroom Mic"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/devices", pairReq2)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	var pairedDev2 struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &pairedDev2); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()

	// 1. Submit capture with Token 1 but conflicting device_id = Device 2 ID -> Rejected with 400
	spoofedCapture := validate.CaptureRequest{
		CaptureID:  "cap_spoof_1",
		DeviceID:   pairedDev2.ID,
		Transcript: "Sold two Peak Milk. Thank you.",
		OccurredAt: &now,
	}
	body, _ := json.Marshal(spoofedCapture)
	capReq := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	capReq.Header.Set("Content-Type", "application/json")
	capReq.Header.Set("Authorization", "Bearer "+pairedDev.Token)
	wCap := httptest.NewRecorder()
	handler.ServeHTTP(wCap, capReq)
	if wCap.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for conflicting device identifier, got %d: %s", wCap.Code, wCap.Body.String())
	}

	// 2. Submit capture with Token 1 and matching device_id -> Accepted
	validCapture := validate.CaptureRequest{
		CaptureID:  "cap_valid_1",
		DeviceID:   pairedDev.ID,
		Transcript: "Sold two Peak Milk. Thank you.",
		OccurredAt: &now,
	}
	body, _ = json.Marshal(validCapture)
	capReq = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	capReq.Header.Set("Content-Type", "application/json")
	capReq.Header.Set("Authorization", "Bearer "+pairedDev.Token)
	wCap = httptest.NewRecorder()
	handler.ServeHTTP(wCap, capReq)
	if wCap.Code != http.StatusCreated {
		t.Fatalf("expected 201 for matching device identifier, got %d: %s", wCap.Code, wCap.Body.String())
	}

	// 3. Submit capture with Token 1 and empty device_id -> Auto-assigned canonical device_id
	emptyCapture := validate.CaptureRequest{
		CaptureID:  "cap_empty_dev",
		Transcript: "Sold two Peak Milk. Thank you.",
		OccurredAt: &now,
	}
	body, _ = json.Marshal(emptyCapture)
	capReq = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	capReq.Header.Set("Content-Type", "application/json")
	capReq.Header.Set("Authorization", "Bearer "+pairedDev.Token)
	wCap = httptest.NewRecorder()
	handler.ServeHTTP(wCap, capReq)
	if wCap.Code != http.StatusCreated {
		t.Fatalf("expected 201 for empty device_id with token, got %d: %s", wCap.Code, wCap.Body.String())
	}

	// 4. Session caller submitting an unknown device_id -> 400
	unknownCapture := validate.CaptureRequest{
		CaptureID:  "cap_unknown_dev",
		DeviceID:   "dev_non_existent",
		Transcript: "Sold two Peak Milk. Thank you.",
		OccurredAt: &now,
	}
	body, _ = json.Marshal(unknownCapture)
	capReq = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	capReq.Header.Set("Content-Type", "application/json")
	wCap = httptest.NewRecorder()
	handler.ServeHTTP(wCap, capReq)
	if wCap.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown submitted device id, got %d: %s", wCap.Code, wCap.Body.String())
	}

	// 5. Session caller submitting a revoked device_id -> 400
	if err := store.RevokeDevice(ctx, shopID, pairedDev2.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	revokedCapture := validate.CaptureRequest{
		CaptureID:  "cap_revoked_dev",
		DeviceID:   pairedDev2.ID,
		Transcript: "Sold two Peak Milk. Thank you.",
		OccurredAt: &now,
	}
	body, _ = json.Marshal(revokedCapture)
	capReq = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	capReq.Header.Set("Content-Type", "application/json")
	wCap = httptest.NewRecorder()
	handler.ServeHTTP(wCap, capReq)
	if wCap.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for revoked submitted device id, got %d: %s", wCap.Code, wCap.Body.String())
	}
}

func TestDeviceCredentialRotation(t *testing.T) {
	_, handler, _ := setupTestApp(t)

	// Pair device
	req := httptest.NewRequest(http.MethodPost, "/api/devices", bytes.NewBufferString(`{"name":"Front Desk"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("failed to pair: %d", w.Code)
	}

	var dev struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dev); err != nil {
		t.Fatal(err)
	}

	// Verify old token can authenticate
	capReq := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(`{"transcript":"Sold one item. Thank you."}`))
	capReq.Header.Set("Content-Type", "application/json")
	capReq.Header.Set("Authorization", "Bearer "+dev.Token)
	wCap := httptest.NewRecorder()
	handler.ServeHTTP(wCap, capReq)
	if wCap.Code != http.StatusCreated {
		t.Fatalf("expected 201 before rotation, got %d: %s", wCap.Code, wCap.Body.String())
	}

	// Rotate token
	rotReq := httptest.NewRequest(http.MethodPost, "/api/devices/"+dev.ID+"/rotate", nil)
	wRot := httptest.NewRecorder()
	handler.ServeHTTP(wRot, rotReq)
	if wRot.Code != http.StatusOK {
		t.Fatalf("failed to rotate device token: %d: %s", wRot.Code, wRot.Body.String())
	}

	var rotResp struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(wRot.Body.Bytes(), &rotResp); err != nil {
		t.Fatal(err)
	}
	if rotResp.Token == "" || rotResp.Token == dev.Token {
		t.Fatalf("expected a new distinct token upon rotation")
	}

	// Verify old token is now rejected (401)
	capReqOld := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(`{"transcript":"Sold one item. Thank you."}`))
	capReqOld.Header.Set("Content-Type", "application/json")
	capReqOld.Header.Set("Authorization", "Bearer "+dev.Token)
	wOld := httptest.NewRecorder()
	handler.ServeHTTP(wOld, capReqOld)
	if wOld.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 using old token after rotation, got %d", wOld.Code)
	}

	// Verify new token succeeds
	capReqNew := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(`{"transcript":"Sold one item. Thank you."}`))
	capReqNew.Header.Set("Content-Type", "application/json")
	capReqNew.Header.Set("Authorization", "Bearer "+rotResp.Token)
	wNew := httptest.NewRecorder()
	handler.ServeHTTP(wNew, capReqNew)
	if wNew.Code != http.StatusCreated {
		t.Fatalf("expected 201 using new token, got %d: %s", wNew.Code, wNew.Body.String())
	}
}

func TestProductArchivingAndUnarchiving(t *testing.T) {
	store, handler, shopID := setupTestApp(t)
	ctx := context.Background()

	// 1. Create a product
	prod := models.Product{
		ID:        "prd_sugar",
		ShopID:    shopID,
		Name:      "Dangote Sugar 1kg",
		SKU:       "SUGAR-1KG",
		Aliases:   []string{"Sugar", "Sweet Sugar"},
		Price:     models.Money{Amount: 120000, Currency: "NGN"},
		Quantity:  50,
		BaseUnit:  models.UnitPack,
		Packaging: []models.PackagingRelation{},
	}
	if err := store.InsertProduct(ctx, prod); err != nil {
		t.Fatal(err)
	}

	// Verify product matches before archiving
	catalogue, _ := store.ListProducts(ctx, shopID)
	match := validate.MatchProduct("Sugar", catalogue)
	if match.Product == nil || match.Product.ID != "prd_sugar" {
		t.Fatalf("expected match before archive, got %+v", match)
	}

	// 2. Archive product via API
	req := httptest.NewRequest(http.MethodPost, "/api/products/prd_sugar/archive", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to archive product: %d: %s", w.Code, w.Body.String())
	}

	var archivedProd models.Product
	if err := json.Unmarshal(w.Body.Bytes(), &archivedProd); err != nil {
		t.Fatal(err)
	}
	if archivedProd.ArchivedAt == nil {
		t.Fatalf("expected archived_at to be set")
	}

	// 3. Verify archived product is excluded from speech matching
	catalogue, _ = store.ListProducts(ctx, shopID)
	matchArchived := validate.MatchProduct("Sugar", catalogue)
	if matchArchived.Product != nil {
		t.Fatalf("archived product should not match speech recognition, but got %+v", matchArchived)
	}

	// 4. Verify product remains queryable in inventory list with archived_at indicator
	invReq := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	wInv := httptest.NewRecorder()
	handler.ServeHTTP(wInv, invReq)
	if wInv.Code != http.StatusOK {
		t.Fatalf("failed to get inventory: %d", wInv.Code)
	}
	var invResp struct {
		Products []models.Product `json:"products"`
	}
	if err := json.Unmarshal(wInv.Body.Bytes(), &invResp); err != nil {
		t.Fatal(err)
	}
	foundArchived := false
	for _, p := range invResp.Products {
		if p.ID == "prd_sugar" && p.IsArchived() {
			foundArchived = true
		}
	}
	if !foundArchived {
		t.Fatalf("expected archived product to still be listed in inventory")
	}

	// 5. Unarchive product
	unarchReq := httptest.NewRequest(http.MethodPost, "/api/products/prd_sugar/unarchive", nil)
	wUnarch := httptest.NewRecorder()
	handler.ServeHTTP(wUnarch, unarchReq)
	if wUnarch.Code != http.StatusOK {
		t.Fatalf("failed to unarchive product: %d: %s", wUnarch.Code, wUnarch.Body.String())
	}

	// Verify it matches speech recognition again
	catalogue, _ = store.ListProducts(ctx, shopID)
	matchRestored := validate.MatchProduct("Sugar", catalogue)
	if matchRestored.Product == nil || matchRestored.Product.ID != "prd_sugar" {
		t.Fatalf("expected match restored after unarchiving, got %+v", matchRestored)
	}
}

func TestMultiLineReceiptGroupingAndReceiptActions(t *testing.T) {
	store, handler, shopID := setupTestApp(t)
	ctx := context.Background()

	// Seed products
	_ = store.InsertProduct(ctx, models.Product{
		ID:        "prd_peak",
		ShopID:    shopID,
		Name:      "Peak Milk 400g",
		SKU:       "PEAK-400",
		Aliases:   []string{"Peak", "Milk"},
		Price:     models.Money{Amount: 350000, Currency: "NGN"},
		Quantity:  20,
		BaseUnit:  models.UnitPiece,
		Packaging: []models.PackagingRelation{},
	})
	_ = store.InsertProduct(ctx, models.Product{
		ID:        "prd_indomie",
		ShopID:    shopID,
		Name:      "Indomie Instant Noodles 70g",
		SKU:       "IND-70",
		Aliases:   []string{"Indomie", "Noodles"},
		Price:     models.Money{Amount: 25000, Currency: "NGN"},
		Quantity:  100,
		BaseUnit:  models.UnitPack,
		Packaging: []models.PackagingRelation{},
	})

	now := time.Now().UTC()

	// 1. Submit a multi-line restock capture
	capturePayload := validate.CaptureRequest{
		CaptureID:  "receipt_2026_001",
		Transcript: "Received 5 piece of Peak Milk. Received 10 pack of Indomie. Stock received.",
		OccurredAt: &now,
	}
	body, _ := json.Marshal(capturePayload)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("failed capture: %d: %s", w.Code, w.Body.String())
	}

	var capResp struct {
		CaptureID    string               `json:"capture_id"`
		ReceiptID    string               `json:"receipt_id"`
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &capResp); err != nil {
		t.Fatal(err)
	}

	if capResp.ReceiptID != "receipt_2026_001" {
		t.Fatalf("expected ReceiptID to be receipt_2026_001, got %s", capResp.ReceiptID)
	}
	if len(capResp.Transactions) != 2 {
		t.Fatalf("expected 2 line transactions, got %d", len(capResp.Transactions))
	}
	for _, txn := range capResp.Transactions {
		if txn.ReceiptID != "receipt_2026_001" {
			t.Fatalf("expected transaction ReceiptID to be receipt_2026_001, got %s", txn.ReceiptID)
		}
	}

	// 2. Query transactions by receipt_id
	filterReq := httptest.NewRequest(http.MethodGet, "/api/transactions?receipt_id=receipt_2026_001", nil)
	wFilter := httptest.NewRecorder()
	handler.ServeHTTP(wFilter, filterReq)
	if wFilter.Code != http.StatusOK {
		t.Fatalf("failed to filter transactions: %d", wFilter.Code)
	}
	var filterResp struct {
		Transactions []models.Transaction `json:"transactions"`
		Total        int                  `json:"total"`
	}
	if err := json.Unmarshal(wFilter.Body.Bytes(), &filterResp); err != nil {
		t.Fatal(err)
	}
	if filterResp.Total != 2 {
		t.Fatalf("expected 2 transactions from receipt filter, got %d", filterResp.Total)
	}

	// 3. GET /api/receipts/{receipt_id}
	rcptReq := httptest.NewRequest(http.MethodGet, "/api/receipts/receipt_2026_001", nil)
	wRcpt := httptest.NewRecorder()
	handler.ServeHTTP(wRcpt, rcptReq)
	if wRcpt.Code != http.StatusOK {
		t.Fatalf("failed to fetch receipt: %d: %s", wRcpt.Code, wRcpt.Body.String())
	}
	var rcptResp struct {
		ReceiptID      string               `json:"receipt_id"`
		Transactions   []models.Transaction `json:"transactions"`
		TotalCount     int                  `json:"total_count"`
		ConfirmedCount int                  `json:"confirmed_count"`
	}
	if err := json.Unmarshal(wRcpt.Body.Bytes(), &rcptResp); err != nil {
		t.Fatal(err)
	}
	if rcptResp.ReceiptID != "receipt_2026_001" || rcptResp.TotalCount != 2 {
		t.Fatalf("unexpected receipt response: %+v", rcptResp)
	}

	// 4. Test multi-line capture with held lines and receipt-wide confirmation
	heldCapture := validate.CaptureRequest{
		CaptureID:  "receipt_held_002",
		Transcript: "Received 3 piece of Peak. Received 5 unknown widget. Stock received.",
		OccurredAt: &now,
	}
	body, _ = json.Marshal(heldCapture)
	req = httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("failed capture: %d", w.Code)
	}

	// The unknown widget line is in review_required
	// Test receipt-wide reject
	rejReq := httptest.NewRequest(http.MethodPost, "/api/receipts/receipt_held_002/reject", bytes.NewBufferString(`{"notes":"unknown delivery rejected"}`))
	rejReq.Header.Set("Content-Type", "application/json")
	wRej := httptest.NewRecorder()
	handler.ServeHTTP(wRej, rejReq)
	if wRej.Code != http.StatusOK {
		t.Fatalf("failed to reject receipt: %d: %s", wRej.Code, wRej.Body.String())
	}
}

func TestReviewCorrectionsAndDecisionAuditTrail(t *testing.T) {
	store, handler, shopID := setupTestApp(t)
	ctx := context.Background()

	// Seed product
	_ = store.InsertProduct(ctx, models.Product{
		ID:        "prd_peak_400",
		ShopID:    shopID,
		Name:      "Peak Milk 400g",
		SKU:       "PEAK-400",
		Aliases:   []string{"Peak"},
		Price:     models.Money{Amount: 350000, Currency: "NGN"},
		Quantity:  10,
		BaseUnit:  models.UnitPiece,
		Packaging: []models.PackagingRelation{},
	})

	now := time.Now().UTC()

	// 1. Submit a capture with missing anchor or low confidence so it goes to review
	// Spoken mention "Sold two mystery item" -> product unknown, holds for review
	capReq := validate.CaptureRequest{
		CaptureID:  "cap_review_test",
		Transcript: "Sold two unknown thing for 7000 Naira. Thank you.",
		OccurredAt: &now,
	}
	body, _ := json.Marshal(capReq)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("failed capture: %d: %s", w.Code, w.Body.String())
	}

	var capResp struct {
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &capResp); err != nil {
		t.Fatal(err)
	}
	if len(capResp.Transactions) == 0 {
		t.Fatalf("expected held transaction")
	}
	heldTxn := capResp.Transactions[0]
	if heldTxn.Status != models.StatusReviewRequired {
		t.Fatalf("expected review_required, got %s", heldTxn.Status)
	}

	// 2. Perform "Edit & Confirm": assign product Peak Milk, quantity 2, unit piece, notes "Verified with counter staff"
	editPayload := map[string]any{
		"product_id": "prd_peak_400",
		"quantity":   2,
		"unit":       "piece",
		"amount": map[string]any{
			"amount":   700000,
			"currency": "NGN",
		},
		"notes": "Verified with counter staff",
	}
	editBody, _ := json.Marshal(editPayload)
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/transactions/"+heldTxn.ID+"/confirm", bytes.NewReader(editBody))
	confirmReq.Header.Set("Content-Type", "application/json")
	wConfirm := httptest.NewRecorder()
	handler.ServeHTTP(wConfirm, confirmReq)
	if wConfirm.Code != http.StatusOK {
		t.Fatalf("failed to confirm transaction: %d: %s", wConfirm.Code, wConfirm.Body.String())
	}

	// 3. GET /api/transactions/{id}/decision -> inspect decision audit record
	decReq := httptest.NewRequest(http.MethodGet, "/api/transactions/"+heldTxn.ID+"/decision", nil)
	wDec := httptest.NewRecorder()
	handler.ServeHTTP(wDec, decReq)
	if wDec.Code != http.StatusOK {
		t.Fatalf("failed to fetch decision: %d: %s", wDec.Code, wDec.Body.String())
	}

	var decision storage.ReviewDecision
	if err := json.Unmarshal(wDec.Body.Bytes(), &decision); err != nil {
		t.Fatal(err)
	}

	if decision.Decision != storage.DecisionCorrectedAndConfirmed {
		t.Fatalf("expected DecisionCorrectedAndConfirmed, got %s", decision.Decision)
	}
	if decision.Notes != "Verified with counter staff" {
		t.Fatalf("expected notes preserved, got %q", decision.Notes)
	}
	if decision.CorrectedValues.ProductID != "prd_peak_400" || decision.CorrectedValues.Quantity != 2 {
		t.Fatalf("unexpected corrected values: %+v", decision.CorrectedValues)
	}
	if decision.OriginalValues.ProductID != "" {
		t.Fatalf("original product id should have been empty: %+v", decision.OriginalValues)
	}

	// 4. Check inventory was decreased by 2 (10 - 2 = 8)
	prod, err := store.GetProductByID(ctx, shopID, "prd_peak_400")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Quantity != 8 {
		t.Fatalf("expected stock 8 after confirmation, got %d", prod.Quantity)
	}
}
