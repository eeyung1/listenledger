package storage

import (
	"context"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

func TestStore_PurgeAllTranscripts(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -7)

	// Old transaction in shop a (10 days ago) with transcript and audio
	txOldA := models.Transaction{
		ID:          "tx_old_a",
		ShopID:      "a",
		Type:        models.TypeSale,
		ProductID:   "amilo",
		ProductName: "Milo",
		Quantity:    1,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 200000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		Transcript:  "One Milo pack",
		AudioData:   "data:audio/webm;base64,GkXfo59ChoEBQveBAULygQ8=",
		OccurredAt:  now.AddDate(0, 0, -10),
		CreatedAt:   now.AddDate(0, 0, -10),
	}
	if err := s.InsertTransaction(ctx, txOldA); err != nil {
		t.Fatalf("insert txOldA: %v", err)
	}

	// Old transaction in shop b (15 days ago) with transcript
	txOldB := models.Transaction{
		ID:          "tx_old_b",
		ShopID:      "b",
		Type:        models.TypeSale,
		ProductID:   "bmilo",
		ProductName: "Milo",
		Quantity:    2,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 400000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		Transcript:  "Two Milo please",
		OccurredAt:  now.AddDate(0, 0, -15),
		CreatedAt:   now.AddDate(0, 0, -15),
	}
	if err := s.InsertTransaction(ctx, txOldB); err != nil {
		t.Fatalf("insert txOldB: %v", err)
	}

	// Recent transaction in shop a (2 days ago) with transcript and audio
	txRecentA := models.Transaction{
		ID:          "tx_recent_a",
		ShopID:      "a",
		Type:        models.TypeSale,
		ProductID:   "amilo",
		ProductName: "Milo",
		Quantity:    3,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 600000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		Transcript:  "Three Milo packs now",
		AudioData:   "data:audio/webm;base64,GkXfo59ChoEBQveBAULygQ8=",
		OccurredAt:  now.AddDate(0, 0, -2),
		CreatedAt:   now.AddDate(0, 0, -2),
	}
	if err := s.InsertTransaction(ctx, txRecentA); err != nil {
		t.Fatalf("insert txRecentA: %v", err)
	}

	// Count candidates before purge
	count, err := s.CountRetentionCandidates(ctx, cutoff)
	if err != nil {
		t.Fatalf("CountRetentionCandidates: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 candidates before purge, got %d", count)
	}

	// Execute purge
	purged, err := s.PurgeAllTranscripts(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeAllTranscripts: %v", err)
	}
	if purged != 2 {
		t.Fatalf("expected 2 purged rows, got %d", purged)
	}

	// Verify old transactions are purged
	gotOldA, err := s.GetTransactionByID(ctx, "a", "tx_old_a")
	if err != nil {
		t.Fatalf("get tx_old_a: %v", err)
	}
	if gotOldA.Transcript != "" {
		t.Errorf("expected empty transcript on old tx a, got %q", gotOldA.Transcript)
	}
	if gotOldA.AudioData != "" {
		t.Errorf("expected empty audio_data on old tx a, got %q", gotOldA.AudioData)
	}
	// Check financial and audit trail integrity
	if gotOldA.Amount.Amount != 200000 || gotOldA.Quantity != 1 || gotOldA.Status != models.StatusConfirmed {
		t.Errorf("financial data corrupted on old tx a: %+v", gotOldA)
	}

	gotOldB, err := s.GetTransactionByID(ctx, "b", "tx_old_b")
	if err != nil {
		t.Fatalf("get tx_old_b: %v", err)
	}
	if gotOldB.Transcript != "" {
		t.Errorf("expected empty transcript on old tx b, got %q", gotOldB.Transcript)
	}
	if gotOldB.Amount.Amount != 400000 || gotOldB.Quantity != 2 {
		t.Errorf("financial data corrupted on old tx b: %+v", gotOldB)
	}

	// Verify recent transaction is completely preserved
	gotRecentA, err := s.GetTransactionByID(ctx, "a", "tx_recent_a")
	if err != nil {
		t.Fatalf("get tx_recent_a: %v", err)
	}
	if gotRecentA.Transcript != "Three Milo packs now" {
		t.Errorf("recent tx transcript altered: got %q", gotRecentA.Transcript)
	}
	if gotRecentA.AudioData != "data:audio/webm;base64,GkXfo59ChoEBQveBAULygQ8=" {
		t.Errorf("recent tx audio_data altered: got %q", gotRecentA.AudioData)
	}

	// Count candidates after purge
	countAfter, err := s.CountRetentionCandidates(ctx, cutoff)
	if err != nil {
		t.Fatalf("CountRetentionCandidates after: %v", err)
	}
	if countAfter != 0 {
		t.Fatalf("expected 0 candidates after purge, got %d", countAfter)
	}

	// Subsequent purge with same cutoff purges 0 rows
	purgedSecond, err := s.PurgeAllTranscripts(ctx, cutoff)
	if err != nil {
		t.Fatalf("second PurgeAllTranscripts: %v", err)
	}
	if purgedSecond != 0 {
		t.Fatalf("expected 0 rows on second purge, got %d", purgedSecond)
	}
}

func TestStore_PurgeAllTranscripts_Validation(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// Zero timestamp error
	if _, err := s.PurgeAllTranscripts(ctx, time.Time{}); err == nil {
		t.Error("expected error with zero timestamp on PurgeAllTranscripts")
	}
	if _, err := s.CountRetentionCandidates(ctx, time.Time{}); err == nil {
		t.Error("expected error with zero timestamp on CountRetentionCandidates")
	}

	// Nil store error
	var nilStore *Store
	if _, err := nilStore.PurgeAllTranscripts(ctx, time.Now()); err == nil {
		t.Error("expected error with nil store on PurgeAllTranscripts")
	}
	if _, err := nilStore.CountRetentionCandidates(ctx, time.Now()); err == nil {
		t.Error("expected error with nil store on CountRetentionCandidates")
	}
}
