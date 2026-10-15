package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

// openTestPostgres connects to the PostgreSQL test instance if configured via
// TEST_POSTGRES_URL or DATABASE_URL. It creates an isolated, dedicated schema
// for the test to ensure complete isolation and registers a cleanup handler.
func openTestPostgres(t *testing.T) *Store {
	t.Helper()

	pgURL := os.Getenv("TEST_POSTGRES_URL")
	if pgURL == "" {
		pgURL = os.Getenv("DATABASE_URL")
		if !strings.HasPrefix(pgURL, "postgres://") && !strings.HasPrefix(pgURL, "postgresql://") {
			t.Skip("TEST_POSTGRES_URL not configured; skipping live PostgreSQL integration test")
		}
	}

	rootDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("failed to open root postgres connection: %v", err)
	}
	defer rootDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rootDB.PingContext(ctx); err != nil {
		t.Skipf("cannot ping postgres (%v); skipping live PostgreSQL integration test", err)
	}

	schemaName := fmt.Sprintf("test_schema_%d", time.Now().UnixNano())
	if _, err := rootDB.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName)); err != nil {
		t.Fatalf("failed to create test schema %s: %v", schemaName, err)
	}

	sep := "?"
	if strings.Contains(pgURL, "?") {
		sep = "&"
	}
	testDSN := fmt.Sprintf("%s%ssearch_path=%s", pgURL, sep, schemaName)

	store, err := OpenPostgres(testDSN)
	if err != nil {
		cleanDB, _ := sql.Open("pgx", pgURL)
		if cleanDB != nil {
			_, _ = cleanDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
			_ = cleanDB.Close()
		}
		t.Fatalf("OpenPostgres failed: %v", err)
	}

	t.Cleanup(func() {
		_ = store.Close()
		cleanDB, err := sql.Open("pgx", pgURL)
		if err == nil {
			defer cleanDB.Close()
			_, _ = cleanDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
		}
	})

	return store
}

func seedPostgresShop(t *testing.T, s *Store, shopID, ownerID string) {
	t.Helper()
	ctx := context.Background()
	err := s.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  ownerID,
			Email:    fmt.Sprintf("%s@example.com", ownerID),
			ShopID:   shopID,
			ShopName: fmt.Sprintf("Shop %s", shopID),
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "test_hash",
		RecoveryHash: "recovery_hash",
	})
	if err != nil && !errors.Is(err, ErrConflict) {
		t.Fatalf("seedPostgresShop failed: %v", err)
	}
}

func TestPostgres_MigrationIdempotency(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	// Initial migration is executed by OpenPostgres
	var count1 int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count1); err != nil {
		t.Fatalf("query schema_migrations count failed: %v", err)
	}
	if count1 < 5 {
		t.Fatalf("expected at least 5 migrations for postgres, got %d", count1)
	}

	// Re-run migration; must be idempotent and succeed without error
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("repeat migration failed: %v", err)
	}

	var count2 int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count2); err != nil {
		t.Fatalf("query schema_migrations count after repeat failed: %v", err)
	}
	if count1 != count2 {
		t.Fatalf("expected migration count to remain %d, got %d", count1, count2)
	}
}

func TestPostgres_ConstraintsAndNegativeStock(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_const"
	seedPostgresShop(t, s, shopID, "own_pg_const")

	p := models.Product{
		ID:       "prd_pg_sugar",
		ShopID:   shopID,
		Name:     "St. Louis Sugar",
		SKU:      "SUGAR-01",
		Price:    models.Money{Amount: 150000, Currency: "NGN"},
		Quantity: 5,
		BaseUnit: models.UnitPack,
	}
	if err := s.InsertProduct(ctx, p); err != nil {
		t.Fatalf("insert product failed: %v", err)
	}

	// Decrementing more than available stock must return ErrInsufficientStock
	txnFail := models.Transaction{
		ID:          "txn_pg_fail",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    10,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 1500000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	_, err := s.RecordMovement(ctx, txnFail, "key_pg_fail", "hash_pg_fail")
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	// Stock must remain unchanged at 5
	prod, err := s.GetProductByID(ctx, shopID, p.ID)
	if err != nil {
		t.Fatalf("get product failed: %v", err)
	}
	if prod.Quantity != 5 {
		t.Fatalf("expected quantity 5, got %d", prod.Quantity)
	}
}

func TestPostgres_OwnerIdentityAndCrossShopIsolation(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopA := "shop_pg_a"
	shopB := "shop_pg_b"
	seedPostgresShop(t, s, shopA, "own_pg_a")
	seedPostgresShop(t, s, shopB, "own_pg_b")

	prodA := models.Product{
		ID:       "prd_pg_milo_a",
		ShopID:   shopA,
		Name:     "Milo 500g",
		SKU:      "MILO-A",
		Price:    models.Money{Amount: 250000, Currency: "NGN"},
		Quantity: 20,
		BaseUnit: models.UnitPack,
	}
	if err := s.InsertProduct(ctx, prodA); err != nil {
		t.Fatalf("insert prodA failed: %v", err)
	}

	// Shop B cannot see Shop A product in list
	prodsB, err := s.ListProducts(ctx, shopB)
	if err != nil {
		t.Fatalf("list products for shopB failed: %v", err)
	}
	if len(prodsB) != 0 {
		t.Fatalf("expected 0 products for shopB, got %d", len(prodsB))
	}

	// Shop B cannot get Shop A product by ID
	if _, err := s.GetProductByID(ctx, shopB, prodA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-shop product lookup, got %v", err)
	}

	// Sessions isolation
	tokenA := "tok_pg_a_123"
	expiry := time.Now().UTC().Add(24 * time.Hour)
	if err := s.CreateSession(ctx, tokenA, "own_pg_a", "test_hash", expiry); err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	ident, err := s.SessionIdentity(ctx, tokenA)
	if err != nil {
		t.Fatalf("get session identity failed: %v", err)
	}
	if ident.OwnerID != "own_pg_a" || ident.ShopID != shopA {
		t.Fatalf("unexpected session details: %+v", ident)
	}
}

func TestPostgres_DevicePairingAndRevocation(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_dev"
	seedPostgresShop(t, s, shopID, "own_pg_dev")

	devID := "dev_pg_001"
	tokenHash := "hash_secret_token_123"
	now := time.Now().UTC()

	dev := Device{
		ID:        devID,
		ShopID:    shopID,
		Name:      "Counter Mic Pi",
		TokenHash: tokenHash,
		PairedAt:  now,
	}
	if err := s.PairDevice(ctx, dev); err != nil {
		t.Fatalf("pair device failed: %v", err)
	}

	// Lookup active device
	activeDev, err := s.GetDeviceByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get device by token hash failed: %v", err)
	}
	if activeDev.ID != devID || activeDev.RevokedAt != nil {
		t.Fatalf("unexpected active device: %+v", activeDev)
	}

	// Revoke device
	if err := s.RevokeDevice(ctx, shopID, devID, now.Add(time.Minute)); err != nil {
		t.Fatalf("revoke device failed: %v", err)
	}

	// Revoked device will now have RevokedAt set
	revokedDev, err := s.GetDeviceByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get revoked device failed: %v", err)
	}
	if revokedDev.RevokedAt == nil {
		t.Fatalf("expected device to have RevokedAt set, got nil")
	}
}

func TestPostgres_SaleAndPackagingRestock(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_sale_restock"
	seedPostgresShop(t, s, shopID, "own_pg_sale_restock")

	// Milo with packaging conversion: 1 carton = 12 packs
	p := models.Product{
		ID:       "prd_pg_milo_conv",
		ShopID:   shopID,
		Name:     "Milo 500g",
		SKU:      "MILO-CONV",
		Price:    models.Money{Amount: 250000, Currency: "NGN"},
		Quantity: 20,
		BaseUnit: models.UnitPack,
		Packaging: []models.PackagingRelation{
			{From: models.UnitCarton, To: models.UnitPack, Factor: 12},
		},
	}
	if err := s.InsertProduct(ctx, p); err != nil {
		t.Fatalf("insert product failed: %v", err)
	}

	// 1. Sale: 20 units -> sell 2 -> balance 18 with 1 ledger entry
	saleTxn := models.Transaction{
		ID:          "txn_pg_sale_01",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 500000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := s.RecordMovement(ctx, saleTxn, "key_sale_01", "hash_sale_01"); err != nil {
		t.Fatalf("record sale movement failed: %v", err)
	}

	prodAfterSale, err := s.GetProductByID(ctx, shopID, p.ID)
	if err != nil {
		t.Fatalf("get product after sale failed: %v", err)
	}
	if prodAfterSale.Quantity != 18 {
		t.Fatalf("expected quantity 18 after sale of 2 from 20, got %d", prodAfterSale.Quantity)
	}

	// 2. Restock: 3 cartons @ 45,000 NGN purchase cost -> 36 packs added (balance 18 + 36 = 54)
	baseQty, err := models.BaseQuantity(p, 3, models.UnitCarton)
	if err != nil {
		t.Fatalf("base quantity conversion failed: %v", err)
	}
	if baseQty != 36 {
		t.Fatalf("expected 36 packs, got %d", baseQty)
	}

	restockTxn := models.Transaction{
		ID:          "txn_pg_restock_01",
		ShopID:      shopID,
		Type:        models.TypeRestock,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    3,
		Unit:        models.UnitCarton,
		Amount:      models.Money{Amount: 4500000, Currency: "NGN"}, // Purchase cost, not revenue
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := s.RecordMovement(ctx, restockTxn, "key_restock_01", "hash_restock_01"); err != nil {
		t.Fatalf("record restock movement failed: %v", err)
	}

	prodAfterRestock, err := s.GetProductByID(ctx, shopID, p.ID)
	if err != nil {
		t.Fatalf("get product after restock failed: %v", err)
	}
	if prodAfterRestock.Quantity != 54 {
		t.Fatalf("expected quantity 54 after restock (18 + 36), got %d", prodAfterRestock.Quantity)
	}
}

func TestPostgres_ConcurrentPurchasesAntiOversell(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_concurrency"
	seedPostgresShop(t, s, shopID, "own_pg_concurrency")

	// Stock is set to 5
	p := models.Product{
		ID:       "prd_pg_milk_concur",
		ShopID:   shopID,
		Name:     "Peak Milk Tin",
		SKU:      "PEAK-CONCUR",
		Price:    models.Money{Amount: 80000, Currency: "NGN"},
		Quantity: 5,
		BaseUnit: models.UnitPiece,
	}
	if err := s.InsertProduct(ctx, p); err != nil {
		t.Fatalf("insert product failed: %v", err)
	}

	// 12 concurrent buyers each try to buy 1 piece
	// Under row-level SELECT ... FOR UPDATE locking, exactly 5 must succeed and 7 must fail with ErrInsufficientStock
	numGoroutines := 12
	var successfulBuys int32
	var failedBuys int32

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			txn := models.Transaction{
				ID:          fmt.Sprintf("txn_concur_%d", idx),
				ShopID:      shopID,
				Type:        models.TypeSale,
				ProductID:   p.ID,
				ProductName: p.Name,
				Quantity:    1,
				Unit:        models.UnitPiece,
				Amount:      models.Money{Amount: 80000, Currency: "NGN"},
				Status:      models.StatusConfirmed,
				OccurredAt:  time.Now().UTC(),
				CreatedAt:   time.Now().UTC(),
			}
			key := fmt.Sprintf("key_concur_%d", idx)
			hash := fmt.Sprintf("hash_concur_%d", idx)

			_, err := s.RecordMovement(ctx, txn, key, hash)
			if err == nil {
				atomic.AddInt32(&successfulBuys, 1)
			} else if errors.Is(err, ErrInsufficientStock) {
				atomic.AddInt32(&failedBuys, 1)
			} else {
				t.Errorf("unexpected error in concurrent buy: %v", err)
			}
		}(i)
	}

	wg.Wait()

	if successfulBuys != 5 {
		t.Fatalf("expected exactly 5 successful buys, got %d", successfulBuys)
	}
	if failedBuys != int32(numGoroutines-5) {
		t.Fatalf("expected exactly %d failed buys, got %d", numGoroutines-5, failedBuys)
	}

	// Final stock must be strictly 0, never negative
	finalProd, err := s.GetProductByID(ctx, shopID, p.ID)
	if err != nil {
		t.Fatalf("get product failed: %v", err)
	}
	if finalProd.Quantity != 0 {
		t.Fatalf("expected final quantity 0, got %d", finalProd.Quantity)
	}
}

func TestPostgres_MultiLineReceiptAtomicity(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_atomic"
	seedPostgresShop(t, s, shopID, "own_pg_atomic")

	p1 := models.Product{
		ID:       "prd_pg_atom_1",
		ShopID:   shopID,
		Name:     "Product 1",
		SKU:      "ATOM-1",
		Price:    models.Money{Amount: 10000, Currency: "NGN"},
		Quantity: 10,
		BaseUnit: models.UnitPiece,
	}
	p2 := models.Product{
		ID:       "prd_pg_atom_2",
		ShopID:   shopID,
		Name:     "Product 2",
		SKU:      "ATOM-2",
		Price:    models.Money{Amount: 20000, Currency: "NGN"},
		Quantity: 10,
		BaseUnit: models.UnitPiece,
	}
	if err := s.InsertProduct(ctx, p1); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertProduct(ctx, p2); err != nil {
		t.Fatal(err)
	}

	// Receipt with 3 items: Line 1 (buy 2 p1), Line 2 (buy 3 p2), Line 3 (buy 100 p1 -> FAILS)
	rcpID := "rcp_pg_fail"
	now := time.Now().UTC()
	txns := []models.Transaction{
		{
			ID:          "txn_atom_line_1",
			ShopID:      shopID,
			ReceiptID:   rcpID,
			Type:        models.TypeSale,
			ProductID:   p1.ID,
			ProductName: p1.Name,
			Quantity:    2,
			Unit:        models.UnitPiece,
			Amount:      models.Money{Amount: 20000, Currency: "NGN"},
			Status:      models.StatusConfirmed,
			OccurredAt:  now,
			CreatedAt:   now,
		},
		{
			ID:          "txn_atom_line_2",
			ShopID:      shopID,
			ReceiptID:   rcpID,
			Type:        models.TypeSale,
			ProductID:   p2.ID,
			ProductName: p2.Name,
			Quantity:    3,
			Unit:        models.UnitPiece,
			Amount:      models.Money{Amount: 60000, Currency: "NGN"},
			Status:      models.StatusConfirmed,
			OccurredAt:  now,
			CreatedAt:   now,
		},
		{
			ID:          "txn_atom_line_3",
			ShopID:      shopID,
			ReceiptID:   rcpID,
			Type:        models.TypeSale,
			ProductID:   p1.ID,
			ProductName: p1.Name,
			Quantity:    100, // Insufficient stock!
			Unit:        models.UnitPiece,
			Amount:      models.Money{Amount: 1000000, Currency: "NGN"},
			Status:      models.StatusConfirmed,
			OccurredAt:  now,
			CreatedAt:   now,
		},
	}
	keys := []string{"k_atom_1", "k_atom_2", "k_atom_3"}
	hashes := []string{"h_atom_1", "h_atom_2", "h_atom_3"}

	_, err := s.RecordReceiptMovements(ctx, txns, keys, hashes)
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock on failed receipt, got %v", err)
	}

	// Check that lines 1 and 2 were cleanly rolled back
	checkP1, _ := s.GetProductByID(ctx, shopID, p1.ID)
	checkP2, _ := s.GetProductByID(ctx, shopID, p2.ID)
	if checkP1.Quantity != 10 {
		t.Fatalf("expected p1 quantity to remain 10 after rollback, got %d", checkP1.Quantity)
	}
	if checkP2.Quantity != 10 {
		t.Fatalf("expected p2 quantity to remain 10 after rollback, got %d", checkP2.Quantity)
	}

	// Check that no transactions or movements were persisted
	txnsAfter, total, err := s.QueryTransactions(ctx, shopID, TransactionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(txnsAfter) != 0 {
		t.Fatalf("expected 0 persisted transactions after rollback, got total %d", total)
	}
}

func TestPostgres_SQLiteToPostgresLiveTransfer(t *testing.T) {
	pgStore := openTestPostgres(t)
	ctx := context.Background()

	// 1. Create populated SQLite source database
	sqliteStore, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite memory failed: %v", err)
	}
	defer sqliteStore.Close()

	shopID := "shop_transfer_src"
	ownerID := "own_transfer_src"
	seedPostgresShop(t, sqliteStore, shopID, ownerID)

	p := models.Product{
		ID:       "prd_trans_1",
		ShopID:   shopID,
		Name:     "Indomie Instant Noodles",
		SKU:      "INDO-01",
		Price:    models.Money{Amount: 40000, Currency: "NGN"},
		Quantity: 50,
		BaseUnit: models.UnitPack,
	}
	if err := sqliteStore.InsertProduct(ctx, p); err != nil {
		t.Fatal(err)
	}

	txnSale := models.Transaction{
		ID:          "txn_trans_sale",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    5,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 200000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	if _, err := sqliteStore.RecordMovement(ctx, txnSale, "key_trans_sale", "hash_trans_sale"); err != nil {
		t.Fatal(err)
	}

	txnReview := models.Transaction{
		ID:          "txn_trans_rev",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductName: "Uncertain Item",
		Quantity:    1,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 10000, Currency: "NGN"},
		Status:      models.StatusReviewRequired,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	if err := sqliteStore.InsertTransaction(ctx, txnReview); err != nil {
		t.Fatal(err)
	}

	// 2. Transfer SQLite -> PostgreSQL
	report, err := Transfer(ctx, sqliteStore, pgStore, false)
	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}

	if !report.AllMatched {
		t.Fatalf("expected report.AllMatched to be true, report: %+v", report)
	}

	// Verify target counts in PostgreSQL
	dstProd, err := pgStore.GetProductByID(ctx, shopID, p.ID)
	if err != nil {
		t.Fatalf("get product in postgres target failed: %v", err)
	}
	if dstProd.Quantity != 45 {
		t.Fatalf("expected quantity 45 in postgres, got %d", dstProd.Quantity)
	}

	dstSummary, err := pgStore.GetDailySummary(ctx, shopID, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("get daily summary in postgres failed: %v", err)
	}
	if dstSummary.TodaySalesAmount.Amount != 200000 {
		t.Fatalf("expected sales amount 200000 in postgres, got %d", dstSummary.TodaySalesAmount.Amount)
	}
	if dstSummary.PendingReviews != 1 {
		t.Fatalf("expected 1 pending review in postgres, got %d", dstSummary.PendingReviews)
	}
}

func TestPostgres_ConcurrentReviewActions(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_review_concur"
	seedPostgresShop(t, s, shopID, "own_pg_review_concur")

	p := models.Product{
		ID:       "prd_pg_rev_p",
		ShopID:   shopID,
		Name:     "Review Item Product",
		SKU:      "REV-01",
		Price:    models.Money{Amount: 100000, Currency: "NGN"},
		Quantity: 10,
		BaseUnit: models.UnitPiece,
	}
	if err := s.InsertProduct(ctx, p); err != nil {
		t.Fatal(err)
	}

	reviewTxn := models.Transaction{
		ID:          "txn_pg_rev_concur",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    2,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 200000, Currency: "NGN"},
		Status:      models.StatusReviewRequired,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.InsertTransaction(ctx, reviewTxn); err != nil {
		t.Fatal(err)
	}

	// Two concurrent adjudications: one confirms, one rejects
	var wg sync.WaitGroup
	wg.Add(2)
	var confirmedCount int32
	var conflictCount int32

	go func() {
		defer wg.Done()
		_, err := s.ResolveReview(ctx, shopID, reviewTxn.ID, ReviewActionConfirm, nil, "approved", "owner")
		if err == nil {
			atomic.AddInt32(&confirmedCount, 1)
		} else if errors.Is(err, ErrConflict) {
			atomic.AddInt32(&conflictCount, 1)
		}
	}()

	go func() {
		defer wg.Done()
		_, err := s.ResolveReview(ctx, shopID, reviewTxn.ID, ReviewActionReject, nil, "denied", "owner")
		if err == nil {
			atomic.AddInt32(&confirmedCount, 1)
		} else if errors.Is(err, ErrConflict) {
			atomic.AddInt32(&conflictCount, 1)
		}
	}()

	wg.Wait()

	if confirmedCount != 1 {
		t.Fatalf("expected exactly 1 adjudication to succeed, got %d", confirmedCount)
	}
	if conflictCount != 1 {
		t.Fatalf("expected exactly 1 adjudication to conflict, got %d", conflictCount)
	}

	// Stock must have been adjusted at most once
	prod, err := s.GetProductByID(ctx, shopID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prod.Quantity != 8 && prod.Quantity != 10 {
		t.Fatalf("unexpected quantity %d (must be 8 if confirmed or 10 if rejected)", prod.Quantity)
	}
}

func TestPostgres_CaptureReplayDeduplicationAndConflict(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_replay"
	seedPostgresShop(t, s, shopID, "own_pg_replay")

	p := models.Product{
		ID:       "prd_pg_replay_p",
		ShopID:   shopID,
		Name:     "Replay Test Product",
		SKU:      "REP-01",
		Price:    models.Money{Amount: 50000, Currency: "NGN"},
		Quantity: 20,
		BaseUnit: models.UnitPiece,
	}
	if err := s.InsertProduct(ctx, p); err != nil {
		t.Fatal(err)
	}

	txn := models.Transaction{
		ID:          "txn_pg_replay_1",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    1,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 50000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}
	key := "idem_key_001"
	hash := "fingerprint_hash_aaa"

	// First attempt succeeds
	res1, err := s.RecordMovement(ctx, txn, key, hash)
	if err != nil {
		t.Fatalf("first RecordMovement failed: %v", err)
	}
	if res1.Replayed {
		t.Fatalf("expected first call to not be marked replayed")
	}

	prodAfter1, _ := s.GetProductByID(ctx, shopID, p.ID)
	if prodAfter1.Quantity != 19 {
		t.Fatalf("expected quantity 19 after first buy, got %d", prodAfter1.Quantity)
	}

	// Replaying identical key and hash returns original transaction with Replayed=true
	res2, err := s.RecordMovement(ctx, txn, key, hash)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if !res2.Replayed {
		t.Fatalf("expected replayed flag to be true")
	}
	if res2.Transaction.ID != txn.ID {
		t.Fatalf("expected same transaction ID %s, got %s", txn.ID, res2.Transaction.ID)
	}

	// Stock remains 19 (not decremented twice)
	prodAfter2, _ := s.GetProductByID(ctx, shopID, p.ID)
	if prodAfter2.Quantity != 19 {
		t.Fatalf("expected quantity to remain 19, got %d", prodAfter2.Quantity)
	}

	// Replaying with identical key but altered hash returns ErrConflict
	_, err = s.RecordMovement(ctx, txn, key, "different_hash_bbb")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict for altered payload hash, got %v", err)
	}
}

func TestPostgres_SummaryAccuracyAndDayBoundaries(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_summary"
	seedPostgresShop(t, s, shopID, "own_pg_summary")

	now := time.Now().UTC()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	endOfToday := startOfToday.Add(24 * time.Hour)

	// Transaction 1: Sale today
	txnToday := models.Transaction{
		ID:          "txn_sum_today",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductName: "Item A",
		Quantity:    1,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 150000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  startOfToday.Add(2 * time.Hour),
		CreatedAt:   startOfToday.Add(2 * time.Hour),
	}
	if err := s.InsertTransaction(ctx, txnToday); err != nil {
		t.Fatal(err)
	}

	// Transaction 2: Sale yesterday (should be outside today's summary)
	txnYesterday := models.Transaction{
		ID:          "txn_sum_yest",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductName: "Item B",
		Quantity:    2,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 300000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  startOfToday.Add(-2 * time.Hour),
		CreatedAt:   startOfToday.Add(-2 * time.Hour),
	}
	if err := s.InsertTransaction(ctx, txnYesterday); err != nil {
		t.Fatal(err)
	}

	// Transaction 3: Restock today (purchase cost, not sales revenue)
	txnRestock := models.Transaction{
		ID:          "txn_sum_restock",
		ShopID:      shopID,
		Type:        models.TypeRestock,
		ProductName: "Item C",
		Quantity:    10,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 500000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  startOfToday.Add(3 * time.Hour),
		CreatedAt:   startOfToday.Add(3 * time.Hour),
	}
	if err := s.InsertTransaction(ctx, txnRestock); err != nil {
		t.Fatal(err)
	}

	// Transaction 4: Review required today
	txnReview := models.Transaction{
		ID:          "txn_sum_rev",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductName: "Item D",
		Quantity:    1,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 50000, Currency: "NGN"},
		Status:      models.StatusReviewRequired,
		OccurredAt:  startOfToday.Add(4 * time.Hour),
		CreatedAt:   startOfToday.Add(4 * time.Hour),
	}
	if err := s.InsertTransaction(ctx, txnReview); err != nil {
		t.Fatal(err)
	}

	summary, err := s.GetDailySummary(ctx, shopID, startOfToday, endOfToday)
	if err != nil {
		t.Fatalf("GetDailySummary failed: %v", err)
	}

	if summary.TodaySalesCount != 1 {
		t.Errorf("expected today_sales_count 1, got %d", summary.TodaySalesCount)
	}
	if summary.TodaySalesAmount.Amount != 150000 {
		t.Errorf("expected today_sales_amount 150000, got %d", summary.TodaySalesAmount.Amount)
	}
	if summary.PendingReviews != 1 {
		t.Errorf("expected pending_reviews 1, got %d", summary.PendingReviews)
	}
}

func TestPostgres_PurgeTranscripts(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_purge"
	seedPostgresShop(t, s, shopID, "own_pg_purge")

	p := models.Product{
		ID:                "prod_pg_purge_1",
		ShopID:            shopID,
		Name:              "Indomie Box",
		SKU:               "INDO-PURGE",
		Price:             models.Money{Amount: 500000, Currency: "NGN"},
		Quantity:          100,
		BaseUnit:          "box",
		LowStockThreshold: 10,
	}
	if err := s.InsertProduct(ctx, p); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	oldTime := now.AddDate(0, 0, -40)
	recentTime := now.AddDate(0, 0, -5)

	oldTxn := models.Transaction{
		ID:          "txn_pg_purge_old",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    2,
		Unit:        models.Unit(p.BaseUnit),
		Amount:      models.Money{Amount: 1000000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  oldTime,
		CreatedAt:   oldTime,
		Transcript:  "Two Indomie boxes, ten thousand naira. All done.",
	}
	if err := s.InsertTransaction(ctx, oldTxn); err != nil {
		t.Fatal(err)
	}

	recentTxn := models.Transaction{
		ID:          "txn_pg_purge_recent",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    1,
		Unit:        models.Unit(p.BaseUnit),
		Amount:      models.Money{Amount: 500000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  recentTime,
		CreatedAt:   recentTime,
		Transcript:  "One Indomie box, five thousand naira. All done.",
	}
	if err := s.InsertTransaction(ctx, recentTxn); err != nil {
		t.Fatal(err)
	}

	purged, err := s.PurgeTranscripts(ctx, shopID, now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("PurgeTranscripts failed on postgres: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 row purged, got %d", purged)
	}

	gotOld, err := s.GetTransactionByID(ctx, shopID, oldTxn.ID)
	if err != nil {
		t.Fatalf("get old txn: %v", err)
	}
	if gotOld.Transcript != "" {
		t.Errorf("expected empty transcript, got %q", gotOld.Transcript)
	}
	if gotOld.Amount.Amount != 1000000 {
		t.Errorf("expected amount to stay 1000000, got %d", gotOld.Amount.Amount)
	}

	gotRecent, err := s.GetTransactionByID(ctx, shopID, recentTxn.ID)
	if err != nil {
		t.Fatalf("get recent txn: %v", err)
	}
	if gotRecent.Transcript != recentTxn.Transcript {
		t.Errorf("expected recent transcript preserved, got %q", gotRecent.Transcript)
	}
}

func TestPostgres_BackupAndRestoreVerification(t *testing.T) {
	src := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_backup_test"
	ownerID := "own_pg_backup_test"
	seedPostgresShop(t, src, shopID, ownerID)

	// 1. Seed agreed target gates
	gates := models.TargetGates{
		MinFullTransactionAccuracy: 91.0,
		MinSKUAccuracy:             96.0,
		MinQuantityAccuracy:        95.0,
		MinUnitAccuracy:            95.0,
		MinAmountAccuracy:          95.0,
		MaxFalseCaptureRate:        2.5,
		MaxMissedTransactionRate:   4.0,
		MaxCorrectionRate:          9.0,
		MinProtocolComplianceRate:  88.0,
		MaxP95LatencyMs:            1400.0,
	}
	if _, err := src.SetTargetGates(ctx, shopID, gates, "Backup Test Owner"); err != nil {
		t.Fatalf("set target gates: %v", err)
	}

	// 2. Mark staff trained
	trainedAt, err := src.MarkStaffTrained(ctx, shopID)
	if err != nil {
		t.Fatalf("mark staff trained: %v", err)
	}

	// 3. Seed product and inventory
	prod := models.Product{
		ID:                "prd_pg_backup_item",
		ShopID:            shopID,
		Name:              "Peak Evaporated Milk",
		SKU:               "PEAK-EVAP",
		Aliases:           []string{"Peak Milk", "Liquid Peak"},
		Price:             models.Money{Amount: 450000, Currency: "NGN"},
		Quantity:          40,
		BaseUnit:          models.UnitPack,
		LowStockThreshold: 10,
		Packaging: []models.PackagingRelation{
			{From: models.UnitCarton, To: models.UnitPack, Factor: 24},
		},
	}
	if err := src.InsertProduct(ctx, prod); err != nil {
		t.Fatalf("insert product: %v", err)
	}

	// 4. Record sale transaction and stock movement
	saleTxn := models.Transaction{
		ID:          "txn_pg_backup_sale",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   prod.ID,
		ProductName: prod.Name,
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 900000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
		Transcript:  "Two tins Peak milk, nine thousand naira. Thank you.",
	}
	movementRes, err := src.RecordMovement(ctx, saleTxn, "idem_pg_backup_sale", "hash_pg_backup_sale")
	if err != nil {
		t.Fatalf("record movement: %v", err)
	}
	if movementRes.Product.Quantity != 38 {
		t.Fatalf("expected stock 38, got %d", movementRes.Product.Quantity)
	}

	// 5. Create fresh, isolated destination schema (clean restore target)
	pgURL := os.Getenv("TEST_POSTGRES_URL")
	if pgURL == "" {
		pgURL = os.Getenv("DATABASE_URL")
	}
	rootDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open root postgres: %v", err)
	}
	defer rootDB.Close()

	cleanSchema := fmt.Sprintf("clean_restore_%d", time.Now().UnixNano())
	if _, err := rootDB.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", cleanSchema)); err != nil {
		t.Fatalf("create clean restore schema: %v", err)
	}
	t.Cleanup(func() {
		cleanDB, err := sql.Open("pgx", pgURL)
		if err == nil {
			defer cleanDB.Close()
			_, _ = cleanDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", cleanSchema))
		}
	})

	sep := "?"
	if strings.Contains(pgURL, "?") {
		sep = "&"
	}
	dstDSN := fmt.Sprintf("%s%ssearch_path=%s", pgURL, sep, cleanSchema)
	dst, err := OpenPostgres(dstDSN)
	if err != nil {
		t.Fatalf("open destination postgres on clean schema: %v", err)
	}
	defer dst.Close()

	// 6. Execute full logical restore & reconciliation from source into clean target
	report, err := Transfer(ctx, src, dst, false)
	if err != nil {
		t.Fatalf("restore transfer to clean target failed: %v", err)
	}
	if !report.AllMatched {
		t.Errorf("expected all tables and per-shop invariants to match after restore, got report: %+v", report)
	}

	// 7. Verify live application access on the restored store
	restoredShop, err := dst.GetShop(ctx, shopID)
	if err != nil {
		t.Fatalf("dst.GetShop failed: %v", err)
	}
	if restoredShop.Name != "Shop "+shopID {
		t.Errorf("expected shop name 'Shop %s', got %q", shopID, restoredShop.Name)
	}
	if restoredShop.StaffTrainedAt == nil || *restoredShop.StaffTrainedAt != formatTime(trainedAt) {
		t.Errorf("expected staff_trained_at %v, got %v", formatTime(trainedAt), restoredShop.StaffTrainedAt)
	}

	restoredGates, isAgreed, err := dst.GetTargetGates(ctx, shopID)
	if err != nil {
		t.Fatalf("dst.GetTargetGates failed: %v", err)
	}
	if !isAgreed || restoredGates.MinFullTransactionAccuracy != 91.0 {
		t.Errorf("expected restored target gates 91.0%% accuracy with isAgreed=true, got %+v", restoredGates)
	}

	restoredProd, err := dst.GetProductByID(ctx, shopID, prod.ID)
	if err != nil {
		t.Fatalf("dst.GetProductByID failed: %v", err)
	}
	if restoredProd.Quantity != 38 {
		t.Errorf("expected restored product quantity 38, got %d", restoredProd.Quantity)
	}

	now := time.Now().UTC()
	summary, err := dst.GetDailySummary(ctx, shopID, now.Add(-24*time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("dst.GetDailySummary failed: %v", err)
	}
	if summary.TodaySalesAmount.Amount != 900000 {
		t.Errorf("expected restored sales revenue 900000, got %d", summary.TodaySalesAmount.Amount)
	}
}

func TestPostgres_QueryProducts(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()

	shopID := "shop_pg_query"
	seedPostgresShop(t, s, shopID, "own_pg_query")

	now := time.Now().UTC()
	archivedAt := now.Add(-2 * time.Hour)

	prod1 := models.Product{
		ID:       "prd_pg_indomie_70g",
		ShopID:   shopID,
		Name:     "Indomie Instant Noodles 70g",
		SKU:      "INDO-70G",
		Aliases:  []string{"Indomie", "Noodles"},
		Price:    models.Money{Amount: 18000, Currency: "NGN"},
		Quantity: 100,
		BaseUnit: models.UnitPack,
	}
	prod2 := models.Product{
		ID:         "prd_pg_indomie_old",
		ShopID:     shopID,
		Name:       "Indomie Chicken Discontinued",
		SKU:        "INDO-OLD",
		Aliases:    []string{"Old Indomie"},
		Price:      models.Money{Amount: 15000, Currency: "NGN"},
		Quantity:   0,
		BaseUnit:   models.UnitPack,
		ArchivedAt: &archivedAt,
	}
	if err := s.InsertProduct(ctx, prod1); err != nil {
		t.Fatalf("insert prod1: %v", err)
	}
	if err := s.InsertProduct(ctx, prod2); err != nil {
		t.Fatalf("insert prod2: %v", err)
	}

	// 1. Filter active
	active, total, err := s.QueryProducts(ctx, shopID, ProductQuery{Status: "active"})
	if err != nil {
		t.Fatalf("query active: %v", err)
	}
	if total != 1 || len(active) != 1 || active[0].ID != "prd_pg_indomie_70g" {
		t.Errorf("expected 1 active product prd_pg_indomie_70g, got %+v", active)
	}

	// 2. Filter archived
	archived, total, err := s.QueryProducts(ctx, shopID, ProductQuery{Status: "archived"})
	if err != nil {
		t.Fatalf("query archived: %v", err)
	}
	if total != 1 || len(archived) != 1 || archived[0].ID != "prd_pg_indomie_old" {
		t.Errorf("expected 1 archived product prd_pg_indomie_old, got %+v", archived)
	}

	// 3. Search query
	searchRes, total, err := s.QueryProducts(ctx, shopID, ProductQuery{Search: "Noodles"})
	if err != nil {
		t.Fatalf("search noodles: %v", err)
	}
	if total != 1 || len(searchRes) != 1 || searchRes[0].ID != "prd_pg_indomie_70g" {
		t.Errorf("expected 1 match for Noodles alias, got %+v", searchRes)
	}
}

func TestPostgres_RuntimeRequiresCompleteSchema(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()
	if err := s.verifyMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = 7"); err != nil {
		t.Fatal(err)
	}
	if err := s.verifyMigrations(ctx); err == nil {
		t.Fatal("runtime accepted a schema missing migration 7")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version = 7").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("read-only runtime check modified migration history")
	}
}

func TestPostgres_AuthLimiterConcurrentAttempts(t *testing.T) {
	s := openTestPostgres(t)
	ctx := context.Background()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, err := s.AllowAuthAttempt(ctx, "deployment-login", 3)
			if err != nil {
				t.Errorf("auth attempt: %v", err)
				return
			}
			if allowed {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := accepted.Load(); got != 3 {
		t.Fatalf("accepted %d attempts, want 3", got)
	}
	if allowed, err := s.AllowAuthAttempt(ctx, "deployment-login", 3); err != nil || allowed {
		t.Fatalf("exhausted counter: allowed=%v err=%v", allowed, err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE auth_attempts SET expires_at = '2000-01-01T00:00:00.000000000Z'"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.AllowAuthAttempt(ctx, "deployment-login", 3); err != nil || !allowed {
		t.Fatalf("expired counter did not reset: allowed=%v err=%v", allowed, err)
	}
}
