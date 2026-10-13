package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eeyung1/listenledger-backend/models"
)

func TestProductsTemplate(t *testing.T) {
	_, handler, _ := setupCaptureTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/template", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("expected text/csv Content-Type, got %s", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "sku,name,price_amount") {
		t.Fatalf("expected CSV header in template, got:\n%s", body)
	}
	if !strings.Contains(body, "MILO-400") {
		t.Fatalf("expected sample row MILO-400 in template")
	}
}

func TestProductsImportCSV(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)

	csvContent := `sku,name,price_amount,price_currency,quantity,base_unit,aliases,packaging,low_stock_threshold
FANTA-50,Fanta Bottle,20000,NGN,48,bottle,Fanta;Orange Drink,crate:24,10
SPRITE-50,Sprite 50cl,22000,NGN,36,bottle,Sprite;Lime,crate:24,8
`
	req := httptest.NewRequest(http.MethodPost, "/api/products/import", strings.NewReader(csvContent))
	req.Header.Set("Content-Type", "text/csv")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Total    int      `json:"total"`
		Imported int      `json:"imported"`
		Updated  int      `json:"updated"`
		Failed   int      `json:"failed"`
		Errors   []string `json:"errors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res.Total != 2 || res.Imported != 2 || res.Updated != 0 || res.Failed != 0 {
		t.Fatalf("unexpected import result: %+v", res)
	}

	// Verify product exists in storage
	p1, err := store.GetProductBySKU(context.Background(), shopID, "FANTA-50")
	if err != nil || p1.Name != "Fanta Bottle" || p1.Price.Amount != 20000 {
		t.Fatalf("product 1 not properly stored: %+v, err: %v", p1, err)
	}

	// Re-importing with updated price should update rather than insert
	updatedCSV := `sku,name,price_amount,price_currency,quantity,base_unit,aliases,packaging,low_stock_threshold
FANTA-50,Fanta 50cl Bottle,25000,NGN,48,bottle,Fanta;Orange Drink,crate:24,10
`
	req2 := httptest.NewRequest(http.MethodPost, "/api/products/import", strings.NewReader(updatedCSV))
	req2.Header.Set("Content-Type", "text/csv")
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on re-import, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var res2 struct {
		Total    int      `json:"total"`
		Imported int      `json:"imported"`
		Updated  int      `json:"updated"`
		Failed   int      `json:"failed"`
		Errors   []string `json:"errors"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&res2); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res2.Total != 1 || res2.Imported != 0 || res2.Updated != 1 || res2.Failed != 0 {
		t.Fatalf("unexpected second import result: %+v", res2)
	}

	p1Updated, _ := store.GetProductBySKU(context.Background(), shopID, "FANTA-50")
	if p1Updated.Name != "Fanta 50cl Bottle" || p1Updated.Price.Amount != 25000 {
		t.Fatalf("product 1 not updated: %+v", p1Updated)
	}
}

func TestProductsImportJSON(t *testing.T) {
	store, handler, shopID := setupCaptureTest(t)

	type itemPayload struct {
		SKU               string       `json:"sku"`
		Name              string       `json:"name"`
		Price             models.Money `json:"price"`
		Quantity          int          `json:"quantity"`
		BaseUnit          models.Unit  `json:"base_unit"`
		Aliases           []string     `json:"aliases"`
		LowStockThreshold int          `json:"low_stock_threshold"`
	}

	items := []itemPayload{
		{
			SKU:               "BREAD-01",
			Name:              "Family Loaf Bread",
			Price:             models.Money{Amount: 120000, Currency: "NGN"},
			Quantity:          15,
			BaseUnit:          models.UnitPiece,
			Aliases:           []string{"Bread", "Loaf"},
			LowStockThreshold: 3,
		},
	}
	jsonBytes, _ := json.Marshal(items)

	req := httptest.NewRequest(http.MethodPost, "/api/products/import", bytes.NewReader(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	bread, err := store.GetProductBySKU(context.Background(), shopID, "BREAD-01")
	if err != nil || bread.Name != "Family Loaf Bread" {
		t.Fatalf("bread not found in store: %+v, err: %v", bread, err)
	}
}

