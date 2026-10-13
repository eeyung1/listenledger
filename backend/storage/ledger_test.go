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
