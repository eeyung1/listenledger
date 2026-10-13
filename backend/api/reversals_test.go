package api_test

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

func TestReverseTransactionEndpoint(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	// 1. Insert a confirmed sale of 2 packs of Milo
	milo, err := store.GetProductBySKU(ctx, shopID, "MILO-500")
	if err != nil {
		t.Fatal(err)
	}
	initialQty := milo.Quantity

	saleTxn := models.Transaction{
		ID:          "txn_sale_rev_test",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   milo.ID,
		ProductName: milo.Name,
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 500000, Currency: "NGN"},
		Confidence:  models.Confidence{Overall: 1.0},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now(),
		CreatedAt:   time.Now(),
	}
	if _, err := store.RecordMovement(ctx, saleTxn, "key_sale_rev", "hash_sale_rev"); err != nil {
		t.Fatal(err)
	}

	afterSale, _ := store.GetProductByID(ctx, shopID, milo.ID)
	if afterSale.Quantity != initialQty-2 {
		t.Fatalf("expected quantity %d, got %d", initialQty-2, afterSale.Quantity)
	}

	// 2. POST /api/transactions/txn_sale_rev_test/reverse
	body := bytes.NewBufferString(`{"reason":"Customer returned items"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/txn_sale_rev_test/reverse", body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Transaction models.Transaction `json:"transaction"`
		Reversal    models.Transaction `json:"reversal"`
		Product     *models.Product    `json:"product"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	if resp.Transaction.Status != models.StatusCancelled {
		t.Fatalf("expected status cancelled, got %s", resp.Transaction.Status)
	}
	if resp.Product == nil || resp.Product.Quantity != initialQty {
		t.Fatalf("expected stock restored to %d, got %+v", initialQty, resp.Product)
	}

	// 3. Repeated reversal should return HTTP 409 Conflict
	body2 := bytes.NewBufferString(`{"reason":"Duplicate"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/transactions/txn_sale_rev_test/reverse", body2)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on repeated reverse, got %d", rec2.Code)
	}

	// 4. Unknown transaction should return HTTP 404
	req404 := httptest.NewRequest(http.MethodPost, "/api/transactions/nonexistent/reverse", bytes.NewBufferString(`{}`))
	rec404 := httptest.NewRecorder()
	handler.ServeHTTP(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on nonexistent transaction reverse, got %d", rec404.Code)
	}
}

func TestManualTransactionEndpoint(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	milo, err := store.GetProductBySKU(ctx, shopID, "MILO-500")
	if err != nil {
		t.Fatal(err)
	}
	initialQty := milo.Quantity

	// 1. Manual sale with default price calculation (2 packs * 250000 = 500000 NGN)
	payload := `{"type":"sale","product_id":"` + milo.ID + `","quantity":2,"unit":"pack","notes":"Cashier counter fallback"}`
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/manual", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Transaction models.Transaction `json:"transaction"`
		Product     *models.Product    `json:"product"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}

	if res.Transaction.Amount.Amount != 400000 {
		t.Fatalf("expected calculated amount 400000, got %d", res.Transaction.Amount.Amount)
	}
	if res.Product.Quantity != initialQty-2 {
		t.Fatalf("expected stock %d, got %d", initialQty-2, res.Product.Quantity)
	}

	// 2. Manual restock in cartons with packaging conversion (1 carton = 12 packs => +12 packs)
	restockPayload := `{"type":"restock","product_id":"` + milo.ID + `","quantity":1,"unit":"carton","amount":{"amount":2800000,"currency":"NGN"},"notes":"Supplier delivery"}`
	reqRestock := httptest.NewRequest(http.MethodPost, "/api/transactions/manual", bytes.NewBufferString(restockPayload))
	recRestock := httptest.NewRecorder()
	handler.ServeHTTP(recRestock, reqRestock)

	if recRestock.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for restock, got %d: %s", recRestock.Code, recRestock.Body.String())
	}

	var resRestock struct {
		Transaction models.Transaction `json:"transaction"`
		Product     *models.Product    `json:"product"`
	}
	if err := json.Unmarshal(recRestock.Body.Bytes(), &resRestock); err != nil {
		t.Fatal(err)
	}

	if resRestock.Product.Quantity != (initialQty - 2 + 12) {
		t.Fatalf("expected stock %d, got %d", initialQty-2+12, resRestock.Product.Quantity)
	}

	// 3. Test insufficient stock handling on manual sale
	excessivePayload := `{"type":"sale","product_id":"` + milo.ID + `","quantity":9999,"unit":"pack"}`
	reqOOM := httptest.NewRequest(http.MethodPost, "/api/transactions/manual", bytes.NewBufferString(excessivePayload))
	recOOM := httptest.NewRecorder()
	handler.ServeHTTP(recOOM, reqOOM)
	if recOOM.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on insufficient stock, got %d", recOOM.Code)
	}
}

func TestProductMovementsAndStockAdjustmentEndpoints(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)
	ctx := context.Background()

	milo, err := store.GetProductBySKU(ctx, shopID, "MILO-500")
	if err != nil {
		t.Fatal(err)
	}

	// 1. POST /api/products/{id}/adjust (Count audit correction)
	adjustPayload := `{"quantity":15,"reason":"Damaged 5 cans discarded"}`
	reqAdjust := httptest.NewRequest(http.MethodPost, "/api/products/"+milo.ID+"/adjust", bytes.NewBufferString(adjustPayload))
	recAdjust := httptest.NewRecorder()
	handler.ServeHTTP(recAdjust, reqAdjust)

	if recAdjust.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", recAdjust.Code, recAdjust.Body.String())
	}

	var resAdjust struct {
		Product     models.Product     `json:"product"`
		Transaction models.Transaction `json:"transaction"`
	}
	if err := json.Unmarshal(recAdjust.Body.Bytes(), &resAdjust); err != nil {
		t.Fatal(err)
	}

	if resAdjust.Product.Quantity != 15 {
		t.Fatalf("expected adjusted quantity 15, got %d", resAdjust.Product.Quantity)
	}
	if resAdjust.Transaction.Quantity != 5 {
		t.Fatalf("expected audit delta magnitude 5, got %d", resAdjust.Transaction.Quantity)
	}

	// 2. GET /api/products/{id}/movements
	reqMovements := httptest.NewRequest(http.MethodGet, "/api/products/"+milo.ID+"/movements", nil)
	recMovements := httptest.NewRecorder()
	handler.ServeHTTP(recMovements, reqMovements)

	if recMovements.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", recMovements.Code, recMovements.Body.String())
	}

	var resMovements struct {
		ShopID    string `json:"shop_id"`
		ProductID string `json:"product_id"`
		Movements []struct {
			TransactionID string `json:"transaction_id"`
			Delta         int    `json:"delta"`
		} `json:"movements"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(recMovements.Body.Bytes(), &resMovements); err != nil {
		t.Fatal(err)
	}

	if resMovements.Total < 1 || len(resMovements.Movements) < 1 {
		t.Fatalf("expected at least 1 movement, got total %d", resMovements.Total)
	}
	if resMovements.Movements[0].Delta != -5 {
		t.Fatalf("expected first movement delta -5, got %d", resMovements.Movements[0].Delta)
	}
}
