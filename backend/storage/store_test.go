package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
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
