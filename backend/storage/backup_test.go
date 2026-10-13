package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

func TestBackupRestoreAndIntegrity(t *testing.T) {
	ctx := context.Background()
	srcStore, _ := ledgerFixture(t)

	// Insert several movements and a device into shop "a"
	sale := movement("sale-1")
	if _, err := srcStore.RecordMovement(ctx, sale, "cap-1", "hash-1"); err != nil {
		t.Fatalf("record movement: %v", err)
	}

	sale2 := movement("sale-2")
	sale2.OccurredAt = time.Now().Add(-1 * time.Hour)
	if _, err := srcStore.RecordMovement(ctx, sale2, "cap-2", "hash-2"); err != nil {
		t.Fatalf("record movement 2: %v", err)
	}

	devToken := "hash-token-123"
	if err := srcStore.PairDevice(ctx, Device{
		ID:        "dev-1",
		ShopID:    "a",
		Name:      "Counter Mic",
		TokenHash: devToken,
		PairedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("pair device: %v", err)
	}

	// Verify integrity on source
	if err := srcStore.VerifyIntegrity(ctx); err != nil {
		t.Fatalf("source integrity check: %v", err)
	}

	// Create backup
	backupPath := filepath.Join(t.TempDir(), "backup.db")
	if err := srcStore.Backup(ctx, backupPath); err != nil {
		t.Fatalf("backup failed: %v", err)
	}

	// Open the backup in a clean store instance
	restoredStore, err := Open(backupPath)
	if err != nil {
		t.Fatalf("open restored backup: %v", err)
	}
	defer restoredStore.Close()

	// Verify integrity on restored database
	if err := restoredStore.VerifyIntegrity(ctx); err != nil {
		t.Fatalf("restored integrity check: %v", err)
	}

	// Verify restored data
	p, err := restoredStore.GetProductByID(ctx, "a", "amilo")
	if err != nil {
		t.Fatalf("get restored product: %v", err)
	}
	if p.Quantity != 16 { // started at 20, minus 2x 2 packs = 16
		t.Fatalf("restored product quantity = %d, want 16", p.Quantity)
	}

	txns, total, err := restoredStore.QueryTransactions(ctx, "a", TransactionFilter{})
	if err != nil {
		t.Fatalf("query restored transactions: %v", err)
	}
	if total != 2 || len(txns) != 2 {
		t.Fatalf("restored transactions total = %d, len = %d, want 2", total, len(txns))
	}

	// Verify restored device
	dev, err := restoredStore.GetDeviceByTokenHash(ctx, devToken)
	if err != nil {
		t.Fatalf("get restored device: %v", err)
	}
	if dev.Name != "Counter Mic" || dev.ShopID != "a" {
		t.Fatalf("restored device mismatch: %+v", dev)
	}
}

func TestQueryTransactionsFiltering(t *testing.T) {
	ctx := context.Background()
	s, _ := ledgerFixture(t)

	now := time.Now().UTC()
	t1 := movement("txn-sale-1")
	t1.OccurredAt = now.Add(-2 * time.Hour)
	t1.Status = models.StatusConfirmed
	t1.Type = models.TypeSale

	t2 := movement("txn-restock-1")
	t2.OccurredAt = now.Add(-1 * time.Hour)
	t2.Status = models.StatusConfirmed
	t2.Type = models.TypeRestock

	t3 := movement("txn-review-1")
	t3.OccurredAt = now
	t3.Status = models.StatusReviewRequired
	t3.Type = models.TypeSale

	for i, m := range []models.Transaction{t1, t2, t3} {
		if err := s.InsertTransaction(ctx, m); err != nil {
			t.Fatalf("insert transaction %d: %v", i, err)
		}
	}

	// Filter by Type
	saleType := models.TypeSale
	sales, total, err := s.QueryTransactions(ctx, "a", TransactionFilter{Type: &saleType})
	if err != nil || total != 2 || len(sales) != 2 {
		t.Fatalf("filter by TypeSale: got total=%d len=%d err=%v", total, len(sales), err)
	}

	// Filter by Status
	reviewStatus := models.StatusReviewRequired
	reviews, total, err := s.QueryTransactions(ctx, "a", TransactionFilter{Status: &reviewStatus})
	if err != nil || total != 1 || len(reviews) != 1 {
		t.Fatalf("filter by StatusReviewRequired: got total=%d len=%d err=%v", total, len(reviews), err)
	}

	// Filter by Date Range
	fromTime := now.Add(-90 * time.Minute)
	toTime := now.Add(-30 * time.Minute)
	rangeTxns, total, err := s.QueryTransactions(ctx, "a", TransactionFilter{From: &fromTime, To: &toTime})
	if err != nil || total != 1 || len(rangeTxns) != 1 || rangeTxns[0].ID != "txn-restock-1" {
		t.Fatalf("filter by DateRange: got total=%d len=%d err=%v", total, len(rangeTxns), err)
	}

	// Pagination: limit 1, offset 1
	paged, total, err := s.QueryTransactions(ctx, "a", TransactionFilter{Limit: 1, Offset: 1})
	if err != nil || total != 3 || len(paged) != 1 {
		t.Fatalf("pagination: got total=%d len=%d err=%v", total, len(paged), err)
	}
	if paged[0].ID != "txn-restock-1" { // sorted newest first: txn-review-1, txn-restock-1, txn-sale-1
		t.Fatalf("pagination offset 1 got %s, want txn-restock-1", paged[0].ID)
	}
}

func TestUpdateShopSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := ledgerFixture(t)

	// Fetch current
	sh, err := s.GetShop(ctx, "a")
	if err != nil {
		t.Fatalf("get shop: %v", err)
	}
	if sh.Name != "a" || sh.Currency != "NGN" {
		t.Fatalf("initial shop mismatch: %+v", sh)
	}

	newName := "Corner Store Lagos"
	newCurr := "NGN"
	newTz := "Africa/Lagos"
	newThresh := 0.85
	newSale := "Thanks for shopping"
	newRestock := "Goods received"

	updated, err := s.UpdateShopSettings(ctx, "a", UpdateShopSettingsParams{
		Name:                &newName,
		Currency:            &newCurr,
		Timezone:            &newTz,
		ConfidenceThreshold: &newThresh,
		SalePhrase:          &newSale,
		RestockPhrase:       &newRestock,
	})
	if err != nil {
		t.Fatalf("update shop settings: %v", err)
	}
	if updated.Name != newName || updated.ConfidenceThreshold != 0.85 || updated.SalePhrase != newSale || updated.RestockPhrase != newRestock {
		t.Fatalf("updated shop mismatch: %+v", updated)
	}

	// Invalid threshold < 0.5
	badThresh := 0.3
	if _, err := s.UpdateShopSettings(ctx, "a", UpdateShopSettingsParams{ConfidenceThreshold: &badThresh}); err == nil {
		t.Fatal("expected error for threshold < 0.50, got nil")
	}

	// Invalid timezone
	badTz := "Invalid/Zone"
	if _, err := s.UpdateShopSettings(ctx, "a", UpdateShopSettingsParams{Timezone: &badTz}); err == nil {
		t.Fatal("expected error for invalid timezone, got nil")
	}

	// Identical phrases
	samePhrase := "Done"
	if _, err := s.UpdateShopSettings(ctx, "a", UpdateShopSettingsParams{SalePhrase: &samePhrase, RestockPhrase: &samePhrase}); err == nil {
		t.Fatal("expected error for identical phrases, got nil")
	}
}
