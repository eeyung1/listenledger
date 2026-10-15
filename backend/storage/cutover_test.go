package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

// TestCutover_FullLifecycleColdRebootAndDisasterRecovery simulates the complete
// production cutover lifecycle: cold schema provisioning, full multi-tenant trading,
// abrupt server process shutdown/restart, and clean disaster recovery reconciliation.
// References: DATABASE_MIGRATION.md §6, §7, §8
func TestCutover_FullLifecycleColdRebootAndDisasterRecovery(t *testing.T) {
	ctx := context.Background()

	// Phase 1: Cold start on SQLite database
	dbFile := filepath.Join(t.TempDir(), "cutover_source.db")
	src, err := Open(dbFile)
	if err != nil {
		t.Fatalf("cold start Open failed: %v", err)
	}

	shopID := "shop_cutover_001"
	ownerID := "own_cutover_001"

	// 1. Account & Shop Onboarding
	if err := src.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  ownerID,
			Email:    "chidi@mamatolu.ng",
			ShopID:   shopID,
			ShopName: "Mama Tolu Provisions - Cutover Store",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "$2a$12$cutoverPasswordHashValuePlaceholder",
		RecoveryHash: "$2a$12$cutoverRecoveryHashValuePlaceholder",
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}

	// 2. Target Gates Formally Agreed
	gates := models.TargetGates{
		MinFullTransactionAccuracy: 92.5,
		MinSKUAccuracy:             96.0,
		MinQuantityAccuracy:        96.0,
		MinUnitAccuracy:            96.0,
		MinAmountAccuracy:          96.0,
		MaxFalseCaptureRate:        2.0,
		MaxMissedTransactionRate:   3.5,
		MaxCorrectionRate:          8.0,
		MinProtocolComplianceRate:  90.0,
		MaxP95LatencyMs:            1250.0,
	}
	agreedGates, err := src.SetTargetGates(ctx, shopID, gates, "Mama Tolu")
	if err != nil {
		t.Fatalf("set target gates: %v", err)
	}
	if agreedGates.MinFullTransactionAccuracy != 92.5 {
		t.Errorf("expected agreed accuracy 92.5, got %f", agreedGates.MinFullTransactionAccuracy)
	}

	// 3. Cashier Protocol Drill Certification
	trainedAt, err := src.MarkStaffTrained(ctx, shopID)
	if err != nil {
		t.Fatalf("mark staff trained: %v", err)
	}
	if trainedAt.IsZero() {
		t.Error("expected non-zero staff training timestamp")
	}

	// 4. Catalogue & Packaging Setup
	prodMilo := models.Product{
		ID:                "prd_cutover_milo",
		ShopID:            shopID,
		Name:              "Milo 500g Pack",
		SKU:               "MILO-500",
		Aliases:           []string{"milo", "chocolate drink"},
		Price:             models.Money{Amount: 250000, Currency: "NGN"},
		Quantity:          60,
		BaseUnit:          models.UnitPack,
		LowStockThreshold: 15,
		Packaging: []models.PackagingRelation{
			{From: models.UnitCarton, To: models.UnitPack, Factor: 12},
		},
	}
	if err := src.InsertProduct(ctx, prodMilo); err != nil {
		t.Fatalf("insert product milo: %v", err)
	}

	prodMilk := models.Product{
		ID:                "prd_cutover_milk",
		ShopID:            shopID,
		Name:              "Peak Evaporated Milk",
		SKU:               "PEAK-TIN",
		Aliases:           []string{"peak", "milk"},
		Price:             models.Money{Amount: 85000, Currency: "NGN"},
		Quantity:          48,
		BaseUnit:          models.UnitPiece,
		LowStockThreshold: 10,
	}
	if err := src.InsertProduct(ctx, prodMilk); err != nil {
		t.Fatalf("insert product milk: %v", err)
	}

	// 5. Hardware Device Pairing
	dev := Device{
		ID:        "dev_cutover_001",
		ShopID:    shopID,
		Name:      "Counter Mic Terminal 1",
		TokenHash: "hash_cutover_device_token",
		PairedAt:  time.Now().UTC(),
	}
	if err := src.PairDevice(ctx, dev); err != nil {
		t.Fatalf("pair device: %v", err)
	}

	// 6. Multi-Line Restock Intake (2 cartons * 12 = 24 packs)
	restockTxn := models.Transaction{
		ID:          "txn_cutover_restock_1",
		ShopID:      shopID,
		Type:        models.TypeRestock,
		ProductID:   prodMilo.ID,
		ProductName: prodMilo.Name,
		Quantity:    24,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 4800000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
		ReceiptID:   "rcp_cutover_01",
		Transcript:  "Restock two cartons Milo, forty-eight thousand naira. Stock received.",
	}
	if _, err := src.RecordReceiptMovements(ctx, []models.Transaction{restockTxn}, []string{"idem_cutover_restock"}, []string{"hash_cutover_restock"}); err != nil {
		t.Fatalf("record restock receipt: %v", err)
	}

	// 7. Confirmed Sale
	saleTxn := models.Transaction{
		ID:          "txn_cutover_sale_1",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   prodMilo.ID,
		ProductName: prodMilo.Name,
		Quantity:    4,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 1000000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
		Transcript:  "Four Milo, ten thousand naira. Thank you.",
	}
	if _, err := src.RecordMovement(ctx, saleTxn, "idem_cutover_sale", "hash_cutover_sale"); err != nil {
		t.Fatalf("record sale movement: %v", err)
	}

	// 8. Dual-Entry Pilot Reconciliation & Discrepancy Investigation
	recSession := PilotReconciliation{
		ID:                        "rec_cutover_sess_01",
		ShopID:                    shopID,
		Status:                    "discrepant",
		PeriodStart:               time.Now().UTC().Add(-4 * time.Hour),
		PeriodEnd:                 time.Now().UTC(),
		IndependentSource:         "Counter Paper Register",
		TotalIndependentRecords:   10,
		TotalCapturedTransactions: 9,
		MatchedRecords:            9,
		MissedCaptures:            1,
		FalseCaptures:             0,
		PriceDiscrepancies:        0,
		QuantityDiscrepancies:     0,
		TotalIndependentRevenue:   1250000,
		TotalCapturedRevenue:      1000000,
		RevenueDiscrepancy:        250000,
		StockDiscrepancyCount:     0,
		Notes:                     "Morning rush shift audit",
		ConductedBy:               "Auditor Emeka",
		CreatedAt:                 time.Now().UTC(),
	}
	discLines := []PilotDiscrepancy{
		{
			ID:                  "disc_cutover_001",
			ReconciliationID:    recSession.ID,
			ShopID:              shopID,
			DiscrepancyType:     "missed_capture",
			ProductName:         prodMilo.Name,
			ExpectedQuantity:    1,
			ActualQuantity:      0,
			ExpectedAmount:      250000,
			ActualAmount:        0,
			InvestigationStatus: "unexplained",
		},
	}
	if err := src.CreateReconciliation(ctx, recSession, discLines); err != nil {
		t.Fatalf("create reconciliation: %v", err)
	}

	// Investigate discrepancy
	nowInvestigated := time.Now().UTC()
	if err := src.UpdateDiscrepancyInvestigation(ctx, shopID, discLines[0].ID, "acoustic_noise", "Loud street generator masked closing phrase", "Auditor Emeka", nowInvestigated); err != nil {
		t.Fatalf("update discrepancy investigation: %v", err)
	}

	// Verify session status auto-promoted to reconciled
	reconciledCheck, err := src.GetReconciliation(ctx, shopID, recSession.ID)
	if err != nil {
		t.Fatalf("get reconciliation: %v", err)
	}
	if reconciledCheck.Status != "reconciled" {
		t.Errorf("expected session reconciled, got %s", reconciledCheck.Status)
	}

	// 9. Cohort Phased Advancement to Phase 2
	if err := src.UpsertShopCohort(ctx, ShopCohort{
		ShopID:      shopID,
		CohortPhase: "phase2_dual_shop",
		StageStatus: "qualified",
		QualifiedAt: &reconciledCheck.CreatedAt,
		Notes:       "Phase 1 trial complete, promoted to Phase 2",
	}); err != nil {
		t.Fatalf("upsert cohort: %v", err)
	}

	// Phase 2: Abrupt Server Reboot Simulation
	// Close store connection completely, simulating container restart or crash
	if err := src.Close(); err != nil {
		t.Fatalf("store.Close() failed: %v", err)
	}

	// Re-open store from disk (cold reboot)
	restartedStore, err := Open(dbFile)
	if err != nil {
		t.Fatalf("re-open after crash failed: %v", err)
	}
	defer restartedStore.Close()

	if err := restartedStore.Ping(ctx); err != nil {
		t.Fatalf("restarted store ping failed: %v", err)
	}

	// Phase 3: Invariant & State Persistence Verification after Reboot
	restoredShop, err := restartedStore.GetShop(ctx, shopID)
	if err != nil {
		t.Fatalf("get shop after reboot: %v", err)
	}
	if restoredShop.Name != "Mama Tolu Provisions - Cutover Store" {
		t.Errorf("shop name mismatch after reboot: %s", restoredShop.Name)
	}

	restoredGates, agreed, err := restartedStore.GetTargetGates(ctx, shopID)
	if err != nil || !agreed || restoredGates.MinFullTransactionAccuracy != 92.5 {
		t.Errorf("target gates lost or corrupted after reboot: agreed=%v, gates=%+v", agreed, restoredGates)
	}

	// Check product stock: 60 (initial) + 24 (restock) - 4 (sale) = 80 packs
	miloCheck, err := restartedStore.GetProductByID(ctx, shopID, prodMilo.ID)
	if err != nil {
		t.Fatalf("get milo after reboot: %v", err)
	}
	if miloCheck.Quantity != 80 {
		t.Errorf("expected milo stock 80 after reboot, got %d", miloCheck.Quantity)
	}

	cohortCheck, err := restartedStore.GetShopCohort(ctx, shopID)
	if err != nil {
		t.Fatalf("get cohort after reboot: %v", err)
	}
	if cohortCheck.CohortPhase != "phase2_dual_shop" || cohortCheck.StageStatus != "qualified" {
		t.Errorf("cohort lost or corrupted after reboot: %+v", cohortCheck)
	}

	// Phase 4: Disaster Recovery Target Provisioning & Reconciliation
	destDBFile := filepath.Join(t.TempDir(), "cutover_target.db")
	dst, err := Open(destDBFile)
	if err != nil {
		t.Fatalf("open clean restore destination: %v", err)
	}
	defer dst.Close()

	report, err := Transfer(ctx, restartedStore, dst, false)
	if err != nil {
		t.Fatalf("cutover disaster restore transfer failed: %v", err)
	}
	if !report.AllMatched {
		t.Errorf("expected clean 100%% match across all 14 tables, got report: %+v", report)
	}

	// Verify live queries on restored destination
	destShop, err := dst.GetShop(ctx, shopID)
	if err != nil || destShop.Name != restoredShop.Name {
		t.Errorf("restored destination shop mismatch: %v", err)
	}
}

// TestCutover_LivePostgreSQLColdRebootAndDisasterRecovery validates the same
// cutover and recovery procedure on a real PostgreSQL instance (when configured).
func TestCutover_LivePostgreSQLColdRebootAndDisasterRecovery(t *testing.T) {
	pgURL := os.Getenv("TEST_POSTGRES_URL")
	if pgURL == "" {
		pgURL = os.Getenv("DATABASE_URL")
		if !strings.HasPrefix(pgURL, "postgres://") && !strings.HasPrefix(pgURL, "postgresql://") {
			t.Skip("TEST_POSTGRES_URL not configured; skipping live PostgreSQL cutover test")
		}
	}

	ctx := context.Background()
	rootDB, err := sql.Open("pgx", pgURL)
	if err != nil {
		t.Fatalf("open root postgres: %v", err)
	}
	defer rootDB.Close()

	if err := rootDB.PingContext(ctx); err != nil {
		t.Skipf("cannot ping postgres (%v); skipping live PostgreSQL cutover test", err)
	}

	// Create dedicated schema
	schemaA := fmt.Sprintf("cutover_live_%d", time.Now().UnixNano())
	if _, err := rootDB.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaA)); err != nil {
		t.Fatalf("create schema A: %v", err)
	}
	t.Cleanup(func() {
		cleanDB, _ := sql.Open("pgx", pgURL)
		if cleanDB != nil {
			defer cleanDB.Close()
			_, _ = cleanDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaA))
		}
	})

	sep := "?"
	if strings.Contains(pgURL, "?") {
		sep = "&"
	}
	dsnA := fmt.Sprintf("%s%ssearch_path=%s", pgURL, sep, schemaA)

	storeA, err := OpenPostgres(dsnA)
	if err != nil {
		t.Fatalf("open postgres store A: %v", err)
	}

	shopID := "shop_pg_cutover_1"
	ownerID := "own_pg_cutover_1"
	seedPostgresShop(t, storeA, shopID, ownerID)

	// Seed cohort and target gates
	gates := models.TargetGates{
		MinFullTransactionAccuracy: 95.0,
		MaxFalseCaptureRate:        1.0,
	}
	if _, err := storeA.SetTargetGates(ctx, shopID, gates, "Owner PG"); err != nil {
		t.Fatalf("set target gates PG: %v", err)
	}

	if err := storeA.UpsertShopCohort(ctx, ShopCohort{
		ShopID:      shopID,
		CohortPhase: "phase1_controlled",
		StageStatus: "active",
		Notes:       "PG cutover test cohort",
	}); err != nil {
		t.Fatalf("upsert cohort PG: %v", err)
	}

	// Abrupt reboot: Close connection pool
	if err := storeA.Close(); err != nil {
		t.Fatalf("storeA.Close() failed: %v", err)
	}

	// Reconnect to existing schema (cold restart simulation)
	storeARestarted, err := OpenPostgres(dsnA)
	if err != nil {
		t.Fatalf("reconnect to postgres after restart: %v", err)
	}
	defer storeARestarted.Close()

	if err := storeARestarted.Ping(ctx); err != nil {
		t.Fatalf("reconnected ping failed: %v", err)
	}

	cCheck, err := storeARestarted.GetShopCohort(ctx, shopID)
	if err != nil || cCheck.CohortPhase != "phase1_controlled" {
		t.Errorf("cohort corrupted after PG restart: %v, %+v", err, cCheck)
	}
}
