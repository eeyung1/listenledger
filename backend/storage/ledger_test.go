package storage

import (
	"context"
	"errors"
	"fmt"
	"github.com/eeyung1/listenledger-backend/models"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func ledgerFixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	for _, shop := range []string{"a", "b"} {
		if e = s.CreateAccount(ctx, Account{Identity: Identity{OwnerID: "owner" + shop, Email: shop + "@example.com", ShopID: shop, ShopName: shop, Currency: "NGN", Timezone: "Africa/Lagos"}, PasswordHash: "hash", RecoveryHash: "recovery"}); e != nil {
			t.Fatal(e)
		}
		if e = s.InsertProduct(ctx, models.Product{ID: shop + "milo", ShopID: shop, Name: "Milo", SKU: "MILO", Quantity: 20, BaseUnit: models.UnitPack, Price: models.Money{Amount: 200000, Currency: "NGN"}, Packaging: []models.PackagingRelation{{From: models.UnitCarton, To: models.UnitPack, Factor: 12}}}); e != nil {
			t.Fatal(e)
		}
	}
	return s, path
}
func movement(id string) models.Transaction {
	return models.Transaction{ID: id, ShopID: "a", Type: models.TypeSale, ProductID: "amilo", ProductName: "Milo", Quantity: 2, Unit: models.UnitPack, Amount: models.Money{Amount: 400000, Currency: "NGN"}, Status: models.StatusConfirmed, OccurredAt: time.Now(), CreatedAt: time.Now()}
}
func stock(t *testing.T, s *Store, want int) {
	t.Helper()
	p, e := s.GetProductByID(context.Background(), "a", "amilo")
	if e != nil || p.Quantity != want {
		t.Fatalf("stock=%d want=%d error=%v", p.Quantity, want, e)
	}
}
func TestAtomicMovementRetryAndRestart(t *testing.T) {
	s, path := ledgerFixture(t)
	ctx := context.Background()
	sale := movement("sale")
	result, e := s.RecordMovement(ctx, sale, "capture1", "hash1")
	if e != nil || result.Replayed {
		t.Fatal(result, e)
	}
	stock(t, s, 18)
	retry := movement("new-unused-id")
	r, e := s.RecordMovement(ctx, retry, "capture1", "hash1")
	if e != nil || !r.Replayed || r.Transaction.ID != "sale" {
		t.Fatal(r, e)
	}
	stock(t, s, 18)
	if _, e = s.RecordMovement(ctx, retry, "capture1", "different"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	// Fail the ledger insert after the UPDATE; the entire stock update must roll back.
	if _, e = s.RecordMovement(ctx, sale, "capture2", "hash2"); e == nil {
		t.Fatal("duplicate ledger ID accepted")
	}
	stock(t, s, 18)
	restock := movement("restock")
	restock.Type = models.TypeRestock
	restock.Quantity = 3
	restock.Unit = models.UnitCarton
	if _, e = s.RecordMovement(ctx, restock, "restock", "hash3"); e != nil {
		t.Fatal(e)
	}
	stock(t, s, 54)
	s.Close()
	reopened, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if r, e = reopened.RecordMovement(ctx, retry, "capture1", "hash1"); e != nil || !r.Replayed {
		t.Fatal(r, e)
	}
	stock(t, reopened, 54)
}
func TestReviewInvalidAndIsolatedMovements(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()
	held := movement("held")
	held.Status = models.StatusReviewRequired
	if _, e := s.RecordMovement(ctx, held, "held", "h"); e != nil {
		t.Fatal(e)
	}
	stock(t, s, 20)
	cases := []struct {
		name   string
		change func(*models.Transaction)
	}{
		{"stock", func(x *models.Transaction) { x.Quantity = 21 }},
		{"cross-shop", func(x *models.Transaction) { x.ProductID = "bmilo" }},
		{"currency", func(x *models.Transaction) { x.Amount.Currency = "USD" }},
		{"units", func(x *models.Transaction) { x.Unit = models.UnitBottle }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x := movement(tc.name)
			tc.change(&x)
			if _, e := s.RecordMovement(ctx, x, tc.name, tc.name); e == nil {
				t.Fatal("invalid movement accepted")
			}
			stock(t, s, 20)
		})
	}
	rows, e := s.GetTransactions(ctx, "a", 500)
	if e != nil || len(rows) != 1 {
		t.Fatal(len(rows), e)
	}
}
func TestConcurrentRetriesApplyOnce(t *testing.T) {
	s, _ := ledgerFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := s.RecordMovement(context.Background(), movement(fmt.Sprint(i)), "same-capture", "same-hash")
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	stock(t, s, 18)
	var count int
	if e := s.db.QueryRow(`SELECT COUNT(*) FROM stock_movements`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

func TestResolveReview(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// 1. Insert a review_required sale
	heldSale := movement("held_sale")
	heldSale.Status = models.StatusReviewRequired
	if _, err := s.RecordMovement(ctx, heldSale, "req_held_sale", "hash_held_sale"); err != nil {
		t.Fatal(err)
	}
	stock(t, s, 20) // unaffected

	// 2. Cross-shop check: shop "b" cannot confirm shop "a"'s review transaction
	if _, err := s.ResolveReview(ctx, "b", "held_sale", ReviewActionConfirm, nil, "user_b", "unauthorized"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-shop confirm, got %v", err)
	}
	stock(t, s, 20)

	// 3. Confirm the held sale
	res, err := s.ResolveReview(ctx, "a", "held_sale", ReviewActionConfirm, nil, "user_a", "verified sale")
	if err != nil {
		t.Fatal(err)
	}
	if res.Transaction.Status != models.StatusConfirmed {
		t.Fatalf("expected status confirmed, got %s", res.Transaction.Status)
	}
	stock(t, s, 18) // decreased by 2

	// Verify review decision was recorded
	dec, err := s.GetReviewDecision(ctx, "a", "held_sale")
	if err != nil {
		t.Fatalf("failed to get review decision: %v", err)
	}
	if dec.Decision != DecisionConfirmed || dec.DecidedBy != "user_a" || dec.Notes != "verified sale" {
		t.Fatalf("unexpected decision record: %+v", dec)
	}

	// Verify stock_movement row exists
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM stock_movements WHERE transaction_id = 'held_sale' AND delta = -2`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected 1 stock movement row, got %d (err: %v)", count, err)
	}

	// 4. Repeated confirmation should fail with ErrConflict (cannot double-confirm)
	if _, err := s.ResolveReview(ctx, "a", "held_sale", ReviewActionConfirm, nil, "user_a", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate confirm, got %v", err)
	}
	stock(t, s, 18) // unchanged

	// 5. Test rejecting a review_required transaction
	heldReject := movement("held_reject")
	heldReject.Status = models.StatusReviewRequired
	if _, err := s.RecordMovement(ctx, heldReject, "req_held_reject", "hash_held_reject"); err != nil {
		t.Fatal(err)
	}
	stock(t, s, 18) // unaffected

	rejRes, err := s.ResolveReview(ctx, "a", "held_reject", ReviewActionReject, nil, "auditor", "unrecognized chatter")
	if err != nil {
		t.Fatal(err)
	}
	if rejRes.Transaction.Status != models.StatusRejected {
		t.Fatalf("expected status rejected, got %s", rejRes.Transaction.Status)
	}
	stock(t, s, 18) // still unaffected

	decRej, err := s.GetReviewDecision(ctx, "a", "held_reject")
	if err != nil {
		t.Fatalf("failed to get reject review decision: %v", err)
	}
	if decRej.Decision != DecisionRejected || decRej.DecidedBy != "auditor" || decRej.Notes != "unrecognized chatter" {
		t.Fatalf("unexpected reject decision record: %+v", decRej)
	}

	// Cannot reject or confirm again
	if _, err := s.ResolveReview(ctx, "a", "held_reject", ReviewActionConfirm, nil, "user_a", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on confirming rejected transaction, got %v", err)
	}

	// 6. Test restock with packaging conversion and corrections
	heldRestock := movement("held_restock")
	heldRestock.Type = models.TypeRestock
	heldRestock.Status = models.StatusReviewRequired
	heldRestock.Quantity = 1
	heldRestock.Unit = models.UnitPack
	if _, err := s.RecordMovement(ctx, heldRestock, "req_held_restock", "hash_held_restock"); err != nil {
		t.Fatal(err)
	}
	stock(t, s, 18)

	// Confirm with correction: 2 cartons (1 carton = 12 packs => +24 packs)
	correctCartons := &ReviewCorrections{
		Quantity: 2,
		Unit:     models.UnitCarton,
	}
	restockRes, err := s.ResolveReview(ctx, "a", "held_restock", ReviewActionConfirm, correctCartons, "manager", "fixed carton unit")
	if err != nil {
		t.Fatal(err)
	}
	if restockRes.Transaction.Quantity != 2 || restockRes.Transaction.Unit != models.UnitCarton {
		t.Fatalf("corrections not applied: %+v", restockRes.Transaction)
	}
	stock(t, s, 42) // 18 + 24 = 42

	decCorr, err := s.GetReviewDecision(ctx, "a", "held_restock")
	if err != nil {
		t.Fatalf("failed to get corrected review decision: %v", err)
	}
	if decCorr.Decision != DecisionCorrectedAndConfirmed || decCorr.DecidedBy != "manager" || decCorr.Notes != "fixed carton unit" {
		t.Fatalf("unexpected corrected decision record: %+v", decCorr)
	}
	if decCorr.OriginalValues.Quantity != 1 || decCorr.OriginalValues.Unit != models.UnitPack {
		t.Fatalf("original values mismatch: %+v", decCorr.OriginalValues)
	}
	if decCorr.CorrectedValues.Quantity != 2 || decCorr.CorrectedValues.Unit != models.UnitCarton {
		t.Fatalf("corrected values mismatch: %+v", decCorr.CorrectedValues)
	}

	// 7. Insufficient stock on confirm should roll back
	heldHugeSale := movement("held_huge")
	heldHugeSale.Status = models.StatusReviewRequired
	heldHugeSale.Quantity = 100
	if _, err := s.RecordMovement(ctx, heldHugeSale, "req_huge", "hash_huge"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveReview(ctx, "a", "held_huge", ReviewActionConfirm, nil, "user_a", ""); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}
	stock(t, s, 42) // unchanged!
}

func TestRecordReceiptMovementsAtomicity(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// Initial stock is 20
	stock(t, s, 20)

	// 1. Successful multi-line receipt: restock 1 carton (+12) and sell 2 packs (-2)
	line1 := movement("m_line1")
	line1.Type = models.TypeRestock
	line1.Quantity = 1
	line1.Unit = models.UnitCarton

	line2 := movement("m_line2")
	line2.Type = models.TypeSale
	line2.Quantity = 2
	line2.Unit = models.UnitPack

	results, err := s.RecordReceiptMovements(ctx,
		[]models.Transaction{line1, line2},
		[]string{"rcpt_k1", "rcpt_k2"},
		[]string{"hash_k1", "hash_k2"},
	)
	if err != nil {
		t.Fatalf("RecordReceiptMovements failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// 20 + 12 - 2 = 30
	stock(t, s, 30)

	// 2. Replay of receipt
	replayResults, err := s.RecordReceiptMovements(ctx,
		[]models.Transaction{line1, line2},
		[]string{"rcpt_k1", "rcpt_k2"},
		[]string{"hash_k1", "hash_k2"},
	)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if !replayResults[0].Replayed || !replayResults[1].Replayed {
		t.Fatalf("expected both lines replayed: %+v", replayResults)
	}
	stock(t, s, 30) // Stock remains 30

	// 3. Multi-line receipt where line 2 fails (insufficient stock) -> MUST ROLL BACK LINE 1
	failLine1 := movement("fail_line1")
	failLine1.Type = models.TypeRestock
	failLine1.Quantity = 2
	failLine1.Unit = models.UnitCarton // would add +24 if committed

	failLine2 := movement("fail_line2")
	failLine2.Type = models.TypeSale
	failLine2.Quantity = 500
	failLine2.Unit = models.UnitPack // 500 packs is > stock, fails!

	_, err = s.RecordReceiptMovements(ctx,
		[]models.Transaction{failLine1, failLine2},
		[]string{"fail_k1", "fail_k2"},
		[]string{"fail_h1", "fail_h2"},
	)
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got: %v", err)
	}

	// Stock MUST NOT have changed: still 30!
	stock(t, s, 30)

	// failLine1 MUST NOT exist in transactions
	_, err = s.GetTransactionByID(ctx, "a", "fail_line1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected fail_line1 not found, got err: %v", err)
	}

	// 4. Input validation
	if _, err := s.RecordReceiptMovements(ctx, []models.Transaction{line1}, []string{"k1", "k2"}, []string{"h1"}); !errors.Is(err, ErrInvalidMovement) {
		t.Fatalf("expected ErrInvalidMovement for mismatched slice lengths, got: %v", err)
	}
	resEmpty, err := s.RecordReceiptMovements(ctx, nil, nil, nil)
	if err != nil || resEmpty != nil {
		t.Fatalf("expected nil, nil for empty movements, got: %v, %v", resEmpty, err)
	}
}
