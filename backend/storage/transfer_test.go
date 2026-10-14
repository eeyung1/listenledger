package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

func TestTransfer_DryRunAndLiveReconciliation(t *testing.T) {
	ctx := context.Background()

	// 1. Create populated source store
	srcPath := filepath.Join(t.TempDir(), "source.db")
	src, err := Open(srcPath)
	if err != nil {
		t.Fatalf("open source failed: %v", err)
	}
	defer src.Close()

	shopID := "shop_transfer_01"
	ownerID := "owner_transfer_01"

	if err := src.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  ownerID,
			Email:    "merchant@example.com",
			ShopID:   shopID,
			ShopName: "Transfer Supermarket",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "pass_hash",
		RecoveryHash: "rec_hash",
	}); err != nil {
		t.Fatalf("create account failed: %v", err)
	}

	prod1 := models.Product{
		ID:                "prd_milo_01",
		ShopID:            shopID,
		Name:              "Milo 500g",
		SKU:               "MILO-500",
		Aliases:           []string{"milo", "chocolate drink"},
		Price:             models.Money{Amount: 250000, Currency: "NGN"},
		Quantity:          40,
		BaseUnit:          models.UnitPack,
		Packaging:         []models.PackagingRelation{{From: models.UnitCarton, To: models.UnitPack, Factor: 12}},
		LowStockThreshold: 10,
	}
	prod2 := models.Product{
		ID:                "prd_milk_01",
		ShopID:            shopID,
		Name:              "Peak Milk",
		SKU:               "PEAK-TIN",
		Aliases:           []string{"peak", "milk tin"},
		Price:             models.Money{Amount: 85000, Currency: "NGN"},
		Quantity:          15,
		BaseUnit:          models.UnitPiece,
		Packaging:         []models.PackagingRelation{},
		LowStockThreshold: 5,
	}
	if err := src.InsertProduct(ctx, prod1); err != nil {
		t.Fatalf("insert product 1: %v", err)
	}
	if err := src.InsertProduct(ctx, prod2); err != nil {
		t.Fatalf("insert product 2: %v", err)
	}

	// Record confirmed sale
	txn1 := models.Transaction{
		ID:          "txn_sale_01",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   prod1.ID,
		ProductName: prod1.Name,
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 500000, Currency: "NGN"},
		Confidence:  models.Confidence{Overall: 0.95},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		Transcript:  "Two Milo, five thousand naira. Thank you.",
	}
	if _, err := src.RecordMovement(ctx, txn1, "key_sale_01", "hash_sale_01"); err != nil {
		t.Fatalf("record sale movement: %v", err)
	}

	// Record review required transaction
	txnReview := models.Transaction{
		ID:          "txn_rev_01",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   prod2.ID,
		ProductName: prod2.Name,
		Quantity:    1,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 85000, Currency: "NGN"},
		Confidence:  models.Confidence{Overall: 0.60},
		Status:      models.StatusReviewRequired,
		OccurredAt:  time.Now().UTC(),
		Transcript:  "One milk. Thank you.",
	}
	if _, err := src.RecordMovement(ctx, txnReview, "key_rev_01", "hash_rev_01"); err != nil {
		t.Fatalf("record review movement: %v", err)
	}

	// Pair a device
	device := Device{
		ID:        "dev_counter_01",
		ShopID:    shopID,
		Name:      "Counter Pi",
		TokenHash: "token_hash_abc123",
		PairedAt:  time.Now().UTC(),
	}
	if err := src.PairDevice(ctx, device); err != nil {
		t.Fatalf("pair device: %v", err)
	}

	// Create fresh destination store
	dstPath := filepath.Join(t.TempDir(), "destination.db")
	dst, err := Open(dstPath)
	if err != nil {
		t.Fatalf("open destination failed: %v", err)
	}
	defer dst.Close()

	// 2. Test Dry-Run
	dryReport, err := Transfer(ctx, src, dst, true)
	if err != nil {
		t.Fatalf("dry-run transfer returned error: %v", err)
	}
	if !dryReport.DryRun {
		t.Errorf("expected DryRun=true in report")
	}
	if !dryReport.AllMatched {
		t.Errorf("expected AllMatched=true in dry-run report")
	}

	// Destination must remain completely empty after dry-run
	var dstProdCount int
	if err := dst.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&dstProdCount); err != nil {
		t.Fatal(err)
	}
	if dstProdCount != 0 {
		t.Errorf("expected 0 products in destination after dry-run, found %d", dstProdCount)
	}

	// 3. Test Live Transfer
	liveReport, err := Transfer(ctx, src, dst, false)
	if err != nil {
		t.Fatalf("live transfer failed: %v", err)
	}
	if liveReport.DryRun {
		t.Errorf("expected DryRun=false in live report")
	}
	if !liveReport.AllMatched {
		t.Errorf("expected AllMatched=true in live report")
	}

	// Verify all tables match
	for _, tbl := range liveReport.Tables {
		if !tbl.Match {
			t.Errorf("table %s mismatch: src=%d, dst=%d", tbl.TableName, tbl.SrcCount, tbl.DstCount)
		}
	}

	// Verify per-shop reconciliation
	if len(liveReport.Shops) != 1 {
		t.Fatalf("expected 1 shop reconciled, got %d", len(liveReport.Shops))
	}
	shopRec := liveReport.Shops[0]
	if !shopRec.ProductCountMatch || shopRec.SrcProductCount != 2 {
		t.Errorf("shop product count mismatch: %+v", shopRec)
	}
	if !shopRec.StockSumMatch || shopRec.SrcStockSum != 53 { // 40-2=38 + 15 = 53
		t.Errorf("shop stock sum mismatch: %+v", shopRec)
	}
	if !shopRec.SalesRevenueMatch || shopRec.SrcSalesRevenue != 500000 {
		t.Errorf("shop sales revenue mismatch: %+v", shopRec)
	}
	if !shopRec.PendingReviewsMatch || shopRec.SrcPendingReviews != 1 {
		t.Errorf("shop pending reviews mismatch: %+v", shopRec)
	}

	// 4. Verify destination store API access
	destProds, err := dst.ListProducts(ctx, shopID)
	if err != nil {
		t.Fatalf("destination ListProducts failed: %v", err)
	}
	if len(destProds) != 2 {
		t.Fatalf("expected 2 products in destination, got %d", len(destProds))
	}

	destTxns, total, err := dst.QueryTransactions(ctx, shopID, TransactionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("destination QueryTransactions failed: %v", err)
	}
	if total != 2 || len(destTxns) != 2 {
		t.Fatalf("expected 2 transactions in destination, got total=%d, len=%d", total, len(destTxns))
	}

	// Verify device in destination
	dev, err := dst.GetDeviceByID(ctx, shopID, device.ID)
	if err != nil {
		t.Fatalf("destination GetDeviceByID failed: %v", err)
	}
	if dev.Name != device.Name {
		t.Errorf("expected device name %q, got %q", device.Name, dev.Name)
	}
}
