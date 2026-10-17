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

func TestConfirmWithUncataloguedProductOnboarding(t *testing.T) {
	store, handler, shopID := setupTestApp(t)
	ctx := context.Background()

	// 1. Spoken restock of an uncatalogued product with spoken supplier
	capReq := map[string]any{
		"transcript": "From Golden Penny: 5 cartons spaghetti 18000 naira. stock received",
	}
	body, _ := json.Marshal(capReq)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("unexpected capture status: %d: %s", w.Code, w.Body.String())
	}

	var capResp struct {
		Transactions []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &capResp); err != nil {
		t.Fatal(err)
	}
	if len(capResp.Transactions) == 0 {
		t.Fatalf("expected at least 1 held transaction")
	}
	heldTxn := capResp.Transactions[0]
	if heldTxn.Status != models.StatusReviewRequired {
		t.Fatalf("expected review_required for uncatalogued restock, got %s", heldTxn.Status)
	}
	if heldTxn.SupplierName != "Golden Penny" {
		t.Fatalf("expected supplier Golden Penny, got %q", heldTxn.SupplierName)
	}
	if heldTxn.ProductID != "" {
		t.Fatalf("product_id should be empty for uncatalogued product, got %s", heldTxn.ProductID)
	}

	// 2. Perform 1-Tap "+ Add to Catalogue & Confirm Restock"
	confirmPayload := map[string]any{
		"create_product": map[string]any{
			"name":      "Golden Penny Spaghetti 500g",
			"sku":       "GP-SPAG-500",
			"base_unit": "carton",
			"price": map[string]any{
				"amount":   2200000,
				"currency": "NGN",
			},
		},
		"quantity": 5,
		"unit":     "carton",
		"notes":    "Onboarded brand new product from supplier delivery",
	}
	confirmBody, _ := json.Marshal(confirmPayload)
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/transactions/"+heldTxn.ID+"/confirm", bytes.NewReader(confirmBody))
	confirmReq.Header.Set("Content-Type", "application/json")
	wConfirm := httptest.NewRecorder()
	handler.ServeHTTP(wConfirm, confirmReq)
	if wConfirm.Code != http.StatusOK {
		t.Fatalf("failed to confirm transaction with product onboard: %d: %s", wConfirm.Code, wConfirm.Body.String())
	}

	var confirmedTxn models.Transaction
	if err := json.Unmarshal(wConfirm.Body.Bytes(), &confirmedTxn); err != nil {
		t.Fatal(err)
	}
	if confirmedTxn.Status != models.StatusConfirmed {
		t.Fatalf("expected confirmed status, got %s", confirmedTxn.Status)
	}
	if confirmedTxn.ProductID == "" {
		t.Fatalf("expected new product ID to be assigned to confirmed transaction")
	}
	if confirmedTxn.SupplierName != "Golden Penny" {
		t.Fatalf("expected supplier Golden Penny preserved on confirmed transaction, got %q", confirmedTxn.SupplierName)
	}

	// 3. Verify newly created product exists in shop inventory with quantity 5
	newProd, err := store.GetProductByID(ctx, shopID, confirmedTxn.ProductID)
	if err != nil {
		t.Fatalf("failed to load newly onboarded product: %v", err)
	}
	if newProd.Name != "Golden Penny Spaghetti 500g" || newProd.SKU != "GP-SPAG-500" {
		t.Fatalf("unexpected new product details: %+v", newProd)
	}
	if newProd.Quantity != 5 {
		t.Fatalf("expected restocked quantity 5, got %d", newProd.Quantity)
	}
}

func TestSpokenCancellationCaptureE2E(t *testing.T) {
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

	// Initial stock of Peak milk is 10
	prodBefore, err := store.GetProductByID(ctx, shopID, "prd_peak_400")
	if err != nil {
		t.Fatal(err)
	}
	if prodBefore.Quantity != 10 {
		t.Fatalf("expected initial stock 10, got %d", prodBefore.Quantity)
	}

	// Capture with spoken cancellation aloud: "2 Peak milk 7000 naira customer cancelled"
	capReq := map[string]any{
		"transcript": "2 Peak milk 7000 naira customer cancelled",
	}
	body, _ := json.Marshal(capReq)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d: %s", w.Code, w.Body.String())
	}

	var capResp struct {
		RequiresReview bool                 `json:"requires_review"`
		Transactions   []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &capResp); err != nil {
		t.Fatal(err)
	}
	if capResp.RequiresReview {
		t.Fatalf("cancelled transaction should not require review")
	}
	if len(capResp.Transactions) == 0 {
		t.Fatalf("expected cancelled transaction to be recorded")
	}
	cancelledTxn := capResp.Transactions[0]
	if cancelledTxn.Status != models.StatusCancelled {
		t.Fatalf("expected StatusCancelled, got %s", cancelledTxn.Status)
	}

	// Verify inventory was NOT mutated (still 10)
	prodAfter, err := store.GetProductByID(ctx, shopID, "prd_peak_400")
	if err != nil {
		t.Fatal(err)
	}
	if prodAfter.Quantity != 10 {
		t.Fatalf("expected stock to remain 10 after cancelled sale, got %d", prodAfter.Quantity)
	}
}

func TestCompoundMultiItemCaptureE2E(t *testing.T) {
	store, handler, shopID := setupTestApp(t)
	ctx := context.Background()

	prodMilo := models.Product{
		ID:        "prd_milo_01",
		ShopID:    shopID,
		Name:      "Milo 500g",
		SKU:       "MILO-500",
		Aliases:   []string{"Milo", "Milo pack"},
		Price:     models.Money{Amount: 250000, Currency: "NGN"},
		Quantity:  20,
		BaseUnit:  models.UnitPack,
		Packaging: []models.PackagingRelation{},
	}
	prodPeak := models.Product{
		ID:        "prd_peak_01",
		ShopID:    shopID,
		Name:      "Peak Milk 400g",
		SKU:       "PEAK-400",
		Aliases:   []string{"Peak", "Peak milk", "Peak tin"},
		Price:     models.Money{Amount: 350000, Currency: "NGN"},
		Quantity:  15,
		BaseUnit:  models.UnitPack,
		Packaging: []models.PackagingRelation{},
	}
	if err := store.InsertProduct(ctx, prodMilo); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertProduct(ctx, prodPeak); err != nil {
		t.Fatal(err)
	}

	capReq := map[string]any{
		"transcript": "2 Milo and 3 Peak milk thank you",
	}
	body, _ := json.Marshal(capReq)
	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("unexpected capture status %d: %s", w.Code, w.Body.String())
	}

	var capResp struct {
		ReceiptID      string               `json:"receipt_id"`
		RequiresReview bool                 `json:"requires_review"`
		Transactions   []models.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &capResp); err != nil {
		t.Fatal(err)
	}

	if capResp.RequiresReview {
		t.Fatalf("expected multi-item compound cart to be auto-confirmed without review")
	}
	if len(capResp.Transactions) != 2 {
		t.Fatalf("expected 2 transactions in receipt, got %d", len(capResp.Transactions))
	}
	if capResp.ReceiptID == "" {
		t.Fatalf("expected a valid receipt_id grouping the compound items")
	}

	t1, t2 := capResp.Transactions[0], capResp.Transactions[1]
	if t1.ReceiptID != capResp.ReceiptID || t2.ReceiptID != capResp.ReceiptID {
		t.Fatalf("expected both transactions to share receipt_id %s, got %s and %s", capResp.ReceiptID, t1.ReceiptID, t2.ReceiptID)
	}

	if t1.ProductName != "Milo 500g" || t1.Quantity != 2 {
		t.Errorf("expected t1 to be 2 Milo 500g, got %d %s", t1.Quantity, t1.ProductName)
	}
	if t2.ProductName != "Peak Milk 400g" || t2.Quantity != 3 {
		t.Errorf("expected t2 to be 3 Peak Milk 400g, got %d %s", t2.Quantity, t2.ProductName)
	}

	// Verify inventory atomically decremented for both items
	afterMilo, err := store.GetProductByID(ctx, shopID, "prd_milo_01")
	if err != nil {
		t.Fatal(err)
	}
	if afterMilo.Quantity != 18 {
		t.Errorf("expected Milo stock to be 18 (20-2), got %d", afterMilo.Quantity)
	}

	afterPeak, err := store.GetProductByID(ctx, shopID, "prd_peak_01")
	if err != nil {
		t.Fatal(err)
	}
	if afterPeak.Quantity != 12 {
		t.Errorf("expected Peak stock to be 12 (15-3), got %d", afterPeak.Quantity)
	}
}

func TestPilotParksSpokenCreditAndPreservesManualReviewAttribution(t *testing.T) {
	store, handler, shopID := setupTestApp(t)
	ctx := context.Background()

	// 1. Setup catalogue product: Milo 500g at 2500 NGN (250000 kobo)
	err := store.InsertProduct(ctx, models.Product{
		ID:       "prd_milo_credit",
		ShopID:   shopID,
		Name:     "Milo 500g",
		SKU:      "MILO-500",
		Aliases:  []string{"milo", "nestle milo"},
		Price:    models.Money{Amount: 250000, Currency: "NGN"},
		Quantity: 50,
		BaseUnit: "piece",
	})
	if err != nil {
		t.Fatalf("failed to create product: %v", err)
	}

	// 2. Register customer: Emeka Okafor
	now := time.Now().UTC()
	err = store.CreateCustomer(ctx, models.Customer{
		ID:                 "cust_emeka_01",
		ShopID:             shopID,
		Name:               "Emeka Okafor",
		Phone:              "08012345678",
		OutstandingBalance: models.Money{Amount: 0, Currency: "NGN"},
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("failed to create customer: %v", err)
	}

	// 3. Spoken Credit Sale intake via /api/captures
	capturePayload := `{"capture_id":"cap_voice_credit_01","transcript":"two milo 5000 naira for Emeka on credit thank you"}`
	req := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(capturePayload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for parked voice credit sale, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Verify customer's outstanding balance in credit book
	emeka, err := store.GetCustomerByID(ctx, shopID, "cust_emeka_01")
	if err != nil {
		t.Fatal(err)
	}
	if emeka.OutstandingBalance.Amount != 0 {
		t.Errorf("parked credit changed Emeka's balance, got %d", emeka.OutstandingBalance.Amount)
	}

	// 5. Verify customer debts ledger record
	debts, count, err := store.ListCustomerDebts(ctx, shopID, "cust_emeka_01", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(debts) != 0 {
		t.Fatalf("parked credit wrote debts: count %d, len %d", count, len(debts))
	}

	// 6. Preserve manual attribution of a pre-existing credit review.
	err = store.CreateCustomer(ctx, models.Customer{
		ID:                 "cust_ngozi_01",
		ShopID:             shopID,
		Name:               "Mama Ngozi",
		Phone:              "08098765432",
		OutstandingBalance: models.Money{Amount: 0, Currency: "NGN"},
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a credit review created before the pilot freeze. It must remain resolvable.
	reviewTxnID := "txn_legacy_credit_review"
	err = store.InsertTransaction(ctx, models.Transaction{
		ID: reviewTxnID, ShopID: shopID, Type: models.TypeSale,
		ProductID: "prd_milo_credit", ProductName: "Milo 500g",
		Quantity: 2, Unit: "piece", Amount: models.Money{Amount: 500000, Currency: "NGN"},
		Confidence: models.Confidence{Overall: 0.5}, Status: models.StatusReviewRequired,
		Transcript: "two milo 5000 naira for Stranger on credit thank you",
		ReceiptID:  "cap_legacy_credit", OccurredAt: now, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm review and assign to Mama Ngozi
	confirmPayload := `{"customer_id":"cust_ngozi_01","notes":"Assigned to Mama Ngozi after counter clarification"}`
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/transactions/"+reviewTxnID+"/confirm", bytes.NewBufferString(confirmPayload))
	confirmReq.Header.Set("Content-Type", "application/json")
	wConfirm := httptest.NewRecorder()
	handler.ServeHTTP(wConfirm, confirmReq)

	if wConfirm.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for confirming review transaction, got %d: %s", wConfirm.Code, wConfirm.Body.String())
	}

	var confirmedTxn models.Transaction
	if err := json.NewDecoder(wConfirm.Body).Decode(&confirmedTxn); err != nil {
		t.Fatal(err)
	}
	if confirmedTxn.CustomerID != "cust_ngozi_01" {
		t.Errorf("expected confirmed txn customer_id cust_ngozi_01, got %q", confirmedTxn.CustomerID)
	}
	if confirmedTxn.CustomerName != "Mama Ngozi" {
		t.Errorf("expected confirmed txn customer_name Mama Ngozi, got %q", confirmedTxn.CustomerName)
	}

	// Verify Mama Ngozi balance updated
	ngozi, err := store.GetCustomerByID(ctx, shopID, "cust_ngozi_01")
	if err != nil {
		t.Fatal(err)
	}
	if ngozi.OutstandingBalance.Amount != 500000 {
		t.Errorf("expected Mama Ngozi balance to be 500,000, got %d", ngozi.OutstandingBalance.Amount)
	}
}
