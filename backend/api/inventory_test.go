package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

func TestHandleInventory_QueryFiltering(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "inventory_api_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	shopID := "shop_inv_test"
	account := storage.Account{
		Identity: storage.Identity{
			OwnerID:  "own_inv_test",
			Email:    "inv@example.com",
			ShopID:   shopID,
			ShopName: "Inventory Test Shop",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "h",
		RecoveryHash: "r",
	}
	if err := store.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("create account: %v", err)
	}

	handler := api.New(store, nil, func(_ *http.Request) (string, error) {
		return shopID, nil
	}, nil)

	ctx := context.Background()
	now := time.Now().UTC()
	archived := now.Add(-1 * time.Hour)

	products := []models.Product{
		{
			ID:                "prd_1",
			ShopID:            shopID,
			Name:              "Peak Evaporated Milk",
			SKU:               "PEAK-01",
			Aliases:           []string{"Peak Milk", "Liquid Peak"},
			Price:             models.Money{Amount: 450000, Currency: "NGN"},
			Quantity:          50,
			BaseUnit:          models.UnitPack,
			LowStockThreshold: 10,
		},
		{
			ID:                "prd_2",
			ShopID:            shopID,
			Name:              "Milo 500g",
			SKU:               "MILO-500",
			Aliases:           []string{"Nestle Milo"},
			Price:             models.Money{Amount: 250000, Currency: "NGN"},
			Quantity:          5, // low stock!
			BaseUnit:          models.UnitPack,
			LowStockThreshold: 10,
		},
		{
			ID:                "prd_3",
			ShopID:            shopID,
			Name:              "Cabin Biscuits Discontinued",
			SKU:               "CABIN-DISC",
			Aliases:           []string{"Old Biscuits"},
			Price:             models.Money{Amount: 100000, Currency: "NGN"},
			Quantity:          0,
			BaseUnit:          models.UnitPiece,
			LowStockThreshold: 5,
			ArchivedAt:        &archived,
		},
	}
	for _, p := range products {
		if err := store.InsertProduct(ctx, p); err != nil {
			t.Fatalf("insert product %s: %v", p.ID, err)
		}
	}

	// 1. GET /api/inventory with no query params (all products)
	req := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var res1 struct {
		ShopID        string           `json:"shop_id"`
		Products      []models.Product `json:"products"`
		Count         int              `json:"count"`
		Total         int              `json:"total"`
		LowStockCount int              `json:"low_stock_count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res1); err != nil {
		t.Fatalf("decode res1: %v", err)
	}
	if res1.Count != 3 || res1.Total != 3 {
		t.Errorf("expected 3 products in total, got count=%d total=%d", res1.Count, res1.Total)
	}
	if res1.LowStockCount != 1 {
		t.Errorf("expected 1 low stock product, got %d", res1.LowStockCount)
	}

	// 2. GET /api/inventory?status=active
	req2 := httptest.NewRequest(http.MethodGet, "/api/inventory?status=active", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	var res2 struct {
		Products []models.Product `json:"products"`
		Count    int              `json:"count"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&res2); err != nil {
		t.Fatalf("decode res2: %v", err)
	}
	if res2.Total != 2 || res2.Count != 2 {
		t.Errorf("expected 2 active products, got %d", res2.Total)
	}

	// 3. GET /api/inventory?status=archived
	req3 := httptest.NewRequest(http.MethodGet, "/api/inventory?status=archived", nil)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	var res3 struct {
		Products []models.Product `json:"products"`
		Count    int              `json:"count"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(rec3.Body).Decode(&res3); err != nil {
		t.Fatalf("decode res3: %v", err)
	}
	if res3.Total != 1 || res3.Products[0].ID != "prd_3" {
		t.Errorf("expected 1 archived product prd_3, got %+v", res3)
	}

	// 4. GET /api/inventory?q=milo
	req4 := httptest.NewRequest(http.MethodGet, "/api/inventory?q=milo", nil)
	rec4 := httptest.NewRecorder()
	handler.ServeHTTP(rec4, req4)

	var res4 struct {
		Products []models.Product `json:"products"`
		Count    int              `json:"count"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(rec4.Body).Decode(&res4); err != nil {
		t.Fatalf("decode res4: %v", err)
	}
	if res4.Total != 1 || res4.Products[0].ID != "prd_2" {
		t.Errorf("expected 1 product prd_2 matching 'milo', got %+v", res4)
	}

	// 5. GET /api/inventory?limit=1&offset=0
	req5 := httptest.NewRequest(http.MethodGet, "/api/inventory?limit=1&offset=0", nil)
	rec5 := httptest.NewRecorder()
	handler.ServeHTTP(rec5, req5)

	var res5 struct {
		Products []models.Product `json:"products"`
		Count    int              `json:"count"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(rec5.Body).Decode(&res5); err != nil {
		t.Fatalf("decode res5: %v", err)
	}
	if res5.Total != 3 || res5.Count != 1 {
		t.Errorf("expected total=3, count=1 for paged query, got %+v", res5)
	}
}
