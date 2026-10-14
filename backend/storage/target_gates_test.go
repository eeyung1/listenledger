package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

func TestTargetGates_DefaultAndUpsert(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "target_gates.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	shopID := "shop_gates_test_01"

	// 1. Create shop
	err = store.CreateAccount(ctx, storage.Account{
		Identity: storage.Identity{
			OwnerID:  "own_gates_01",
			Email:    "gates@shop.ng",
			ShopID:   shopID,
			ShopName: "Mama Tolu Provisions",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "hashed_pass",
		RecoveryHash: "hashed_recovery",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	// 2. Query target gates when none set -> returns DefaultTargetGates with isAgreed = false
	gates, isAgreed, err := store.GetTargetGates(ctx, shopID)
	if err != nil {
		t.Fatalf("get target gates: %v", err)
	}
	if isAgreed {
		t.Errorf("expected isAgreed = false for new shop")
	}
	if gates.MinFullTransactionAccuracy != models.DefaultTargetGates.MinFullTransactionAccuracy {
		t.Errorf("expected default full transaction accuracy %.1f, got %.1f",
			models.DefaultTargetGates.MinFullTransactionAccuracy, gates.MinFullTransactionAccuracy)
	}

	// 3. Upsert custom agreed target gates
	customGates := models.TargetGates{
		MinFullTransactionAccuracy: 92.5,
		MinSKUAccuracy:             96.0,
		MinQuantityAccuracy:        97.0,
		MinUnitAccuracy:            98.0,
		MinAmountAccuracy:          96.5,
		MaxFalseCaptureRate:        2.0,
		MaxMissedTransactionRate:   4.0,
		MaxCorrectionRate:          8.0,
		MinProtocolComplianceRate:  90.0,
		MaxP95LatencyMs:            1200.0,
	}

	saved, err := store.SetTargetGates(ctx, shopID, customGates, "Mama Tolu Owner")
	if err != nil {
		t.Fatalf("set target gates: %v", err)
	}
	if saved.AgreedAt == nil {
		t.Errorf("expected AgreedAt to be populated")
	}
	if saved.AgreedBy != "Mama Tolu Owner" {
		t.Errorf("expected AgreedBy 'Mama Tolu Owner', got %q", saved.AgreedBy)
	}

	// 4. Retrieve again -> returns updated values and isAgreed = true
	loaded, isAgreed, err := store.GetTargetGates(ctx, shopID)
	if err != nil {
		t.Fatalf("get target gates after set: %v", err)
	}
	if !isAgreed {
		t.Errorf("expected isAgreed = true after saving gates")
	}
	if loaded.MinFullTransactionAccuracy != 92.5 {
		t.Errorf("expected 92.5%% accuracy, got %.1f%%", loaded.MinFullTransactionAccuracy)
	}
	if loaded.MaxP95LatencyMs != 1200.0 {
		t.Errorf("expected 1200ms latency, got %.1f", loaded.MaxP95LatencyMs)
	}

	// 5. Test validation guard: invalid percentage rejects
	invalidGates := customGates
	invalidGates.MinFullTransactionAccuracy = 150.0 // impossible rate
	_, err = store.SetTargetGates(ctx, shopID, invalidGates, "Owner")
	if err == nil {
		t.Errorf("expected error for rate > 100%%, got nil")
	}

	// 6. Test MarkStaffTrained updates Shop.StaffTrainedAt
	shop, err := store.GetShop(ctx, shopID)
	if err != nil {
		t.Fatalf("get shop: %v", err)
	}
	if shop.StaffTrainedAt != nil {
		t.Errorf("expected staff_trained_at to be nil initially, got %v", shop.StaffTrainedAt)
	}

	trainedTime, err := store.MarkStaffTrained(ctx, shopID)
	if err != nil {
		t.Fatalf("mark staff trained: %v", err)
	}
	if trainedTime.IsZero() {
		t.Errorf("expected non-zero trained time")
	}

	shopAfter, err := store.GetShop(ctx, shopID)
	if err != nil {
		t.Fatalf("get shop after training: %v", err)
	}
	if shopAfter.StaffTrainedAt == nil || *shopAfter.StaffTrainedAt == "" {
		t.Errorf("expected staff_trained_at to be set on shop")
	}
}
