package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

func TestStore_MigrationFromEmptyAndIdempotency(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migration_test.db")

	// 1. Initial migration on empty file
	store1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open failed: %v", err)
	}

	// Verify tables exist and schema_migrations is populated
	var migrationCount int
	if err := store1.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("query migrations failed: %v", err)
	}
	if migrationCount < 4 {
		t.Fatalf("expected at least 4 migrations, found %d", migrationCount)
	}
	store1.Close()

	// 2. Open again on the same database file (idempotency test)
	store2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open (idempotent migration) failed: %v", err)
	}
	defer store2.Close()

	var migrationCount2 int
	if err := store2.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount2); err != nil {
		t.Fatalf("query migrations second time failed: %v", err)
	}
	if migrationCount2 != migrationCount {
		t.Fatalf("expected migration count to remain %d, got %d", migrationCount, migrationCount2)
	}
}

func TestStore_ShopIsolation(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// Shop "a" has "amilo", Shop "b" has "bmilo"
	prodsA, err := s.ListProducts(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(prodsA) != 1 || prodsA[0].ID != "amilo" {
		t.Fatalf("expected only amilo for shop a, got %+v", prodsA)
	}

	prodsB, err := s.ListProducts(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(prodsB) != 1 || prodsB[0].ID != "bmilo" {
		t.Fatalf("expected only bmilo for shop b, got %+v", prodsB)
	}

	// Cross-shop lookup by ID
	if _, err := s.GetProductByID(ctx, "a", "bmilo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound looking up shop b product from shop a, got %v", err)
	}
	if _, err := s.GetProductByID(ctx, "b", "amilo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound looking up shop a product from shop b, got %v", err)
	}

	// Insert transaction for shop a
	txnA := movement("txna_iso")
	if _, err := s.RecordMovement(ctx, txnA, "key_txna", "hash_txna"); err != nil {
		t.Fatal(err)
	}

	// Shop b queries transactions
	txnsB, totalB, err := s.QueryTransactions(ctx, "b", TransactionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if totalB != 0 || len(txnsB) != 0 {
		t.Fatalf("expected 0 transactions for shop b, got %d (len %d)", totalB, len(txnsB))
	}

	// Shop b tries to get shop a's transaction by ID
	if _, err := s.GetTransactionByID(ctx, "b", "txna_iso"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound accessing shop a transaction from shop b, got %v", err)
	}
}

func TestStore_EmptySliceJSONEncoding(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// Create an empty shop with no products or transactions
	emptyShopID := "shop_empty"
	err := s.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  "owner_empty",
			Email:    "empty@example.com",
			ShopID:   emptyShopID,
			ShopName: "Empty Shop",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "h",
		RecoveryHash: "r",
	})
	if err != nil {
		t.Fatal(err)
	}

	prods, err := s.ListProducts(ctx, emptyShopID)
	if err != nil {
		t.Fatal(err)
	}
	if prods == nil {
		t.Fatal("expected non-nil empty slice for ListProducts")
	}
	data, _ := json.Marshal(prods)
	if string(data) != "[]" {
		t.Fatalf("expected '[]', got '%s'", string(data))
	}

	txns, total, err := s.QueryTransactions(ctx, emptyShopID, TransactionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || txns == nil {
		t.Fatalf("expected total 0 and non-nil empty slice for QueryTransactions, got %d, %+v", total, txns)
	}
	data, _ = json.Marshal(txns)
	if string(data) != "[]" {
		t.Fatalf("expected '[]', got '%s'", string(data))
	}

	movements, totalMovements, err := s.GetProductMovements(ctx, emptyShopID, "dummy", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if totalMovements != 0 || movements == nil {
		t.Fatalf("expected total 0 and non-nil slice, got %d, %+v", totalMovements, movements)
	}
	data, _ = json.Marshal(movements)
	if string(data) != "[]" {
		t.Fatalf("expected '[]', got '%s'", string(data))
	}
}

func TestStore_DialectAndDriverRouting(t *testing.T) {
	// 1. SQLite dialect detection
	sqliteStore, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	defer sqliteStore.Close()

	if sqliteStore.Dialect() != DialectSQLite {
		t.Errorf("expected DialectSQLite, got %v", sqliteStore.Dialect())
	}
	if !sqliteStore.Dialect().IsSQLite() {
		t.Errorf("expected IsSQLite() to be true")
	}
	if sqliteStore.Dialect().IsPostgres() {
		t.Errorf("expected IsPostgres() to be false for SQLite")
	}

	// 2. PostgreSQL DSN routing
	// Connecting to an unreachable dummy postgres port must return a postgres connection error,
	// verifying that Open routed to OpenPostgres rather than treating it as a SQLite filename.
	_, err = Open("postgres://user:pass@127.0.0.1:54329/testdb?sslmode=disable")
	if err == nil {
		t.Fatal("expected connection error for nonexistent postgres endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("expected error message to mention postgres, got: %v", err)
	}

	// 3. PostgreSQL postgresql:// prefix
	_, err = Open("postgresql://user:pass@127.0.0.1:54329/testdb?sslmode=disable")
	if err == nil {
		t.Fatal("expected connection error for nonexistent postgres endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("expected error message to mention postgres, got: %v", err)
	}
}

func TestStore_PurgeTranscripts(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// Create test shop and product
	shopID := "shop_purge_test"
	if err := s.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  "owner_purge_1",
			Email:    "purge1@example.com",
			ShopID:   shopID,
			ShopName: "Purge Shop",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "h",
		RecoveryHash: "r",
	}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	product := models.Product{
		ID:                "prod_purge_1",
		ShopID:            shopID,
		Name:              "Milo Tin",
		SKU:               "MILO-001",
		Price:             models.Money{Amount: 150000, Currency: "NGN"},
		Quantity:          50,
		BaseUnit:          "tin",
		LowStockThreshold: 5,
	}
	if err := s.InsertProduct(ctx, product); err != nil {
		t.Fatalf("InsertProduct: %v", err)
	}

	// Another shop for tenancy isolation check
	otherShopID := "shop_purge_other"
	if err := s.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  "owner_purge_2",
			Email:    "purge2@example.com",
			ShopID:   otherShopID,
			ShopName: "Other Shop",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "h",
		RecoveryHash: "r",
	}); err != nil {
		t.Fatalf("CreateAccount other: %v", err)
	}
	otherProd := models.Product{
		ID:                "prod_purge_other",
		ShopID:            otherShopID,
		Name:              "Peak Milk",
		SKU:               "PEAK-001",
		Price:             models.Money{Amount: 100000, Currency: "NGN"},
		Quantity:          20,
		BaseUnit:          "tin",
		LowStockThreshold: 5,
	}
	if err := s.InsertProduct(ctx, otherProd); err != nil {
		t.Fatalf("InsertProduct other: %v", err)
	}

	now := time.Now().UTC()
	oldTime := now.AddDate(0, 0, -45)
	recentTime := now.AddDate(0, 0, -5)

	// 1. Old transaction with transcript (shopID)
	oldTxn := models.Transaction{
		ID:          "txn_old",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   product.ID,
		ProductName: product.Name,
		Quantity:    2,
		Unit:        models.Unit(product.BaseUnit),
		Amount:      models.Money{Amount: 300000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  oldTime,
		CreatedAt:   oldTime,
		Transcript:  "Two Milo, three thousand naira. Thank you.",
	}
	if err := s.InsertTransaction(ctx, oldTxn); err != nil {
		t.Fatalf("InsertTransaction old: %v", err)
	}

	// 2. Recent transaction with transcript (shopID)
	recentTxn := models.Transaction{
		ID:          "txn_recent",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   product.ID,
		ProductName: product.Name,
		Quantity:    1,
		Unit:        models.Unit(product.BaseUnit),
		Amount:      models.Money{Amount: 150000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  recentTime,
		CreatedAt:   recentTime,
		Transcript:  "One Milo, fifteen hundred naira. Thank you.",
	}
	if err := s.InsertTransaction(ctx, recentTxn); err != nil {
		t.Fatalf("InsertTransaction recent: %v", err)
	}

	// 3. Old transaction with transcript for otherShopID
	otherOldTxn := models.Transaction{
		ID:          "txn_other_old",
		ShopID:      otherShopID,
		Type:        models.TypeSale,
		ProductID:   otherProd.ID,
		ProductName: otherProd.Name,
		Quantity:    1,
		Unit:        models.Unit(otherProd.BaseUnit),
		Amount:      models.Money{Amount: 100000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  oldTime,
		CreatedAt:   oldTime,
		Transcript:  "One Peak Milk. Thank you.",
	}
	if err := s.InsertTransaction(ctx, otherOldTxn); err != nil {
		t.Fatalf("InsertTransaction other old: %v", err)
	}

	// Purge transcripts older than 30 days for shopID
	cutoff := now.AddDate(0, 0, -30)
	purged, err := s.PurgeTranscripts(ctx, shopID, cutoff)
	if err != nil {
		t.Fatalf("PurgeTranscripts failed: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 row purged, got %d", purged)
	}

	// Verify old transaction transcript was cleared, but all other transaction data remains intact
	gotOld, err := s.GetTransactionByID(ctx, shopID, oldTxn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID old: %v", err)
	}
	if gotOld.Transcript != "" {
		t.Errorf("expected old transaction transcript to be empty, got %q", gotOld.Transcript)
	}
	if gotOld.Amount.Amount != 300000 || gotOld.Quantity != 2 || gotOld.Status != models.StatusConfirmed {
		t.Errorf("expected financial/status records to remain intact, got %+v", gotOld)
	}

	// Verify recent transaction transcript is preserved
	gotRecent, err := s.GetTransactionByID(ctx, shopID, recentTxn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID recent: %v", err)
	}
	if gotRecent.Transcript != recentTxn.Transcript {
		t.Errorf("expected recent transcript %q, got %q", recentTxn.Transcript, gotRecent.Transcript)
	}

	// Verify cross-shop isolation: otherShopID transcript was NOT purged
	gotOtherOld, err := s.GetTransactionByID(ctx, otherShopID, otherOldTxn.ID)
	if err != nil {
		t.Fatalf("GetTransactionByID other old: %v", err)
	}
	if gotOtherOld.Transcript != otherOldTxn.Transcript {
		t.Errorf("expected other shop transcript to remain %q, got %q", otherOldTxn.Transcript, gotOtherOld.Transcript)
	}
}
