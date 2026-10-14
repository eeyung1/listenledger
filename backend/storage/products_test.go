package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

func TestStore_QueryProducts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "products_query_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	shopID := "shop_query_test"
	otherShop := "shop_query_other"

	// Seed shop accounts
	for _, id := range []string{shopID, otherShop} {
		err := store.CreateAccount(ctx, storage.Account{
			Identity: storage.Identity{
				OwnerID:  "own_" + id,
				Email:    id + "@example.com",
				ShopID:   id,
				ShopName: "Shop " + id,
				Currency: "NGN",
				Timezone: "Africa/Lagos",
			},
			PasswordHash: "h",
			RecoveryHash: "r",
		})
		if err != nil {
			t.Fatalf("create account %s: %v", id, err)
		}
	}

	// Seed products
	now := time.Now().UTC()
	archivedTime := now.Add(-1 * time.Hour)

	prods := []models.Product{
		{
			ID:                "prd_milo_500",
			ShopID:            shopID,
			Name:              "Milo 500g Tin",
			SKU:               "MILO-500",
			Aliases:           []string{"Milo", "Nestle Milo"},
			Price:             models.Money{Amount: 250000, Currency: "NGN"},
			Quantity:          30,
			BaseUnit:          models.UnitPack,
			LowStockThreshold: 10,
		},
		{
			ID:                "prd_milo_1kg",
			ShopID:            shopID,
			Name:              "Milo 1kg Refill",
			SKU:               "MILO-1KG",
			Aliases:           []string{"Big Milo", "Milo Refill"},
			Price:             models.Money{Amount: 480000, Currency: "NGN"},
			Quantity:          15,
			BaseUnit:          models.UnitPack,
			LowStockThreshold: 5,
		},
		{
			ID:                "prd_peak_milk",
			ShopID:            shopID,
			Name:              "Peak Evaporated Milk",
			SKU:               "PEAK-EVAP",
			Aliases:           []string{"Peak", "Liquid Peak"},
			Price:             models.Money{Amount: 450000, Currency: "NGN"},
			Quantity:          50,
			BaseUnit:          models.UnitPack,
			LowStockThreshold: 10,
		},
		{
			ID:                "prd_old_biscuit",
			ShopID:            shopID,
			Name:              "Cabin Biscuit Discontinued",
			SKU:               "CABIN-OLD",
			Aliases:           []string{"Old Cabin"},
			Price:             models.Money{Amount: 100000, Currency: "NGN"},
			Quantity:          0,
			BaseUnit:          models.UnitPiece,
			LowStockThreshold: 5,
			ArchivedAt:        &archivedTime,
		},
		{
			ID:       "prd_other_milo",
			ShopID:   otherShop,
			Name:     "Other Shop Milo",
			SKU:      "OTHER-MILO",
			Aliases:  []string{"Milo"},
			Price:    models.Money{Amount: 200000, Currency: "NGN"},
			Quantity: 10,
			BaseUnit: models.UnitPack,
		},
	}

	for _, p := range prods {
		if err := store.InsertProduct(ctx, p); err != nil {
			t.Fatalf("insert product %s: %v", p.ID, err)
		}
	}

	// 1. Query all active (default status="")
	allActive, total, err := store.QueryProducts(ctx, shopID, storage.ProductQuery{Status: "active"})
	if err != nil {
		t.Fatalf("query active: %v", err)
	}
	if total != 3 || len(allActive) != 3 {
		t.Errorf("expected 3 active products, got total=%d, len=%d", total, len(allActive))
	}

	// 2. Query archived only
	archived, total, err := store.QueryProducts(ctx, shopID, storage.ProductQuery{Status: "archived"})
	if err != nil {
		t.Fatalf("query archived: %v", err)
	}
	if total != 1 || len(archived) != 1 {
		t.Errorf("expected 1 archived product, got total=%d, len=%d", total, len(archived))
	}
	if len(archived) > 0 && archived[0].ID != "prd_old_biscuit" {
		t.Errorf("expected prd_old_biscuit, got %s", archived[0].ID)
	}

	// 3. Query all (active + archived)
	all, total, err := store.QueryProducts(ctx, shopID, storage.ProductQuery{Status: "all"})
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if total != 4 || len(all) != 4 {
		t.Errorf("expected 4 total products for shopID, got total=%d, len=%d", total, len(all))
	}

	// 4. Search by name substring "milo"
	miloSearch, total, err := store.QueryProducts(ctx, shopID, storage.ProductQuery{Search: "milo"})
	if err != nil {
		t.Fatalf("search milo: %v", err)
	}
	if total != 2 || len(miloSearch) != 2 {
		t.Errorf("expected 2 milo products, got total=%d, len=%d", total, len(miloSearch))
	}

	// 5. Search by SKU
	skuSearch, total, err := store.QueryProducts(ctx, shopID, storage.ProductQuery{Search: "PEAK-EVAP"})
	if err != nil {
		t.Fatalf("search SKU: %v", err)
	}
	if total != 1 || len(skuSearch) != 1 || skuSearch[0].ID != "prd_peak_milk" {
		t.Errorf("expected prd_peak_milk, got %+v", skuSearch)
	}

	// 6. Search by Alias "Liquid Peak"
	aliasSearch, total, err := store.QueryProducts(ctx, shopID, storage.ProductQuery{Search: "Liquid Peak"})
	if err != nil {
		t.Fatalf("search alias: %v", err)
	}
	if total != 1 || len(aliasSearch) != 1 || aliasSearch[0].ID != "prd_peak_milk" {
		t.Errorf("expected prd_peak_milk via alias, got %+v", aliasSearch)
	}

	// 7. Pagination: limit=1, offset=1
	paged, total, err := store.QueryProducts(ctx, shopID, storage.ProductQuery{Status: "active", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("query paged: %v", err)
	}
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}
	if len(paged) != 1 {
		t.Fatalf("expected 1 product on page, got %d", len(paged))
	}

	// 8. Cross-shop isolation: other shop only sees its own product
	otherProds, total, err := store.QueryProducts(ctx, otherShop, storage.ProductQuery{})
	if err != nil {
		t.Fatalf("query other shop: %v", err)
	}
	if total != 1 || len(otherProds) != 1 || otherProds[0].ID != "prd_other_milo" {
		t.Errorf("expected other shop product only, got %+v", otherProds)
	}
}
