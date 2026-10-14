package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

func TestReverseTransaction_Sale(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// 1. Record a confirmed sale of 2 packs (stock drops 20 -> 18)
	sale := movement("sale_to_reverse")
	if _, err := s.RecordMovement(ctx, sale, "key_sale_rev", "hash_sale_rev"); err != nil {
		t.Fatal(err)
	}
	stock(t, s, 18)

	// 2. Reverse the sale
	res, err := s.ReverseTransaction(ctx, "a", "sale_to_reverse", "Customer returned unopened items")
	if err != nil {
		t.Fatal(err)
	}

	if res.Original.Status != models.StatusCancelled {
		t.Fatalf("expected original status cancelled, got %s", res.Original.Status)
	}
	if res.Reversal.Status != models.StatusCancelled {
		t.Fatalf("expected reversal status cancelled, got %s", res.Reversal.Status)
	}
	if res.Product == nil || res.Product.Quantity != 20 {
		t.Fatalf("expected stock restored to 20, got %+v", res.Product)
	}
	stock(t, s, 20)

	// 3. Reversing again should return ErrConflict (already cancelled)
	if _, err := s.ReverseTransaction(ctx, "a", "sale_to_reverse", "again"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on already cancelled transaction, got %v", err)
	}

	// 4. Cross-shop isolation: shop "b" cannot reverse shop "a"'s transaction
	if _, err := s.ReverseTransaction(ctx, "b", "sale_to_reverse", "hack"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-shop reversal, got %v", err)
	}
}

func TestReverseTransaction_Restock(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// 1. Record a restock of 1 carton (12 packs). Stock: 20 -> 32
	restockTxn := models.Transaction{
		ID:          "restock_to_reverse",
		ShopID:      "a",
		Type:        models.TypeRestock,
		ProductID:   "amilo",
		ProductName: "Milo",
		Quantity:    1,
		Unit:        models.UnitCarton,
		Amount:      models.Money{Amount: 1800000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now(),
		CreatedAt:   time.Now(),
	}
	if _, err := s.RecordMovement(ctx, restockTxn, "key_restock_rev", "hash_restock_rev"); err != nil {
		t.Fatal(err)
	}
	stock(t, s, 32)

	// 2. Reverse the restock (removes 12 packs; stock: 32 -> 20)
	res, err := s.ReverseTransaction(ctx, "a", "restock_to_reverse", "Supplier invoice error")
	if err != nil {
		t.Fatal(err)
	}
	if res.Product.Quantity != 20 {
		t.Fatalf("expected stock reduced back to 20, got %d", res.Product.Quantity)
	}
	stock(t, s, 20)

	// 3. Test insufficient stock protection when reversing a restock
	// Add 50 packs restock, sell 60 packs (stock is 10), then try to reverse the 50 packs restock
	restockBig := models.Transaction{
		ID:          "restock_big",
		ShopID:      "a",
		Type:        models.TypeRestock,
		ProductID:   "amilo",
		ProductName: "Milo",
		Quantity:    50,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 10000000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now(),
		CreatedAt:   time.Now(),
	}
	if _, err := s.RecordMovement(ctx, restockBig, "key_restock_big", "hash_restock_big"); err != nil {
		t.Fatal(err)
	}
	stock(t, s, 70) // 20 + 50 = 70

	// Sell 65 packs: stock becomes 5
	saleBig := models.Transaction{
		ID:          "sale_big",
		ShopID:      "a",
		Type:        models.TypeSale,
		ProductID:   "amilo",
		ProductName: "Milo",
		Quantity:    65,
		Unit:        models.UnitPack,
		Amount:      models.Money{Amount: 13000000, Currency: "NGN"},
		Status:      models.StatusConfirmed,
		OccurredAt:  time.Now(),
		CreatedAt:   time.Now(),
	}
	if _, err := s.RecordMovement(ctx, saleBig, "key_sale_big", "hash_sale_big"); err != nil {
		t.Fatal(err)
	}
	stock(t, s, 5)

	// Trying to reverse restockBig would require removing 50 packs when only 5 exist!
	if _, err := s.ReverseTransaction(ctx, "a", "restock_big", "cannot reverse"); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}
	stock(t, s, 5) // unchanged!
}

func TestGetProductMovements(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// Initial product has opening stock 20, but no movements recorded yet
	movements, total, err := s.GetProductMovements(ctx, "a", "amilo", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(movements) != 0 {
		t.Fatalf("expected 0 movements, got %d (len %d)", total, len(movements))
	}

	// Record 3 sales of 1 pack each
	for i := 1; i <= 3; i++ {
		txn := movement(string(rune('0' + i)))
		txn.Quantity = 1
		if _, err := s.RecordMovement(ctx, txn, string(rune('0'+i)), string(rune('0'+i))); err != nil {
			t.Fatal(err)
		}
	}

	movements, total, err = s.GetProductMovements(ctx, "a", "amilo", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("expected total 3, got %d", total)
	}
	if len(movements) != 2 {
		t.Fatalf("expected page size 2, got %d", len(movements))
	}
	if movements[0].Delta != -1 || movements[1].Delta != -1 {
		t.Fatalf("unexpected deltas: %d, %d", movements[0].Delta, movements[1].Delta)
	}

	// Check offset
	page2, total, err := s.GetProductMovements(ctx, "a", "amilo", 2, 2)
	if err != nil || total != 3 || len(page2) != 1 {
		t.Fatalf("expected page 2 with 1 item, got %d items, total %d, err %v", len(page2), total, err)
	}

	// Cross-shop isolation: shop "b" should see 0 movements for amilo
	bMovements, bTotal, err := s.GetProductMovements(ctx, "b", "amilo", 10, 0)
	if err != nil || bTotal != 0 || len(bMovements) != 0 {
		t.Fatalf("expected 0 movements for shop b on amilo, got %d (err: %v)", bTotal, err)
	}
}

func TestAdjustProductStock(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// Stock is 20
	stock(t, s, 20)

	// Adjust count down to 17 (3 damaged)
	prod, txn, err := s.AdjustProductStock(ctx, "a", "amilo", 17, "Physical audit: 3 packs water damaged")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Quantity != 17 {
		t.Fatalf("expected quantity 17, got %d", prod.Quantity)
	}
	if txn.Quantity != 3 || txn.Type != models.TypeSale {
		t.Fatalf("expected audit txn with 3 units sale, got %+v", txn)
	}
	stock(t, s, 17)

	// Verify movement was created
	movements, total, err := s.GetProductMovements(ctx, "a", "amilo", 10, 0)
	if err != nil || total != 1 || movements[0].Delta != -3 {
		t.Fatalf("expected 1 movement with delta -3, got total %d, delta %v, err %v", total, movements[0].Delta, err)
	}

	// Adjust count up to 25 (found 8 extra packs)
	prod, txn, err = s.AdjustProductStock(ctx, "a", "amilo", 25, "Found extra carton opened in back")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Quantity != 25 {
		t.Fatalf("expected quantity 25, got %d", prod.Quantity)
	}
	if txn.Quantity != 8 || txn.Type != models.TypeRestock {
		t.Fatalf("expected audit txn with 8 units restock, got %+v", txn)
	}
	stock(t, s, 25)

	// Adjust to same quantity (no change)
	prod, txn, err = s.AdjustProductStock(ctx, "a", "amilo", 25, "Count confirmed")
	if err != nil || txn.ID != "" {
		t.Fatalf("expected no-op adjustment, got txn %+v, err %v", txn, err)
	}
}

func TestAdjustStockPackaging(t *testing.T) {
	s, _ := ledgerFixture(t)
	ctx := context.Background()

	// Initial stock is 20 packs. 1 carton = 12 packs.
	stock(t, s, 20)

	// 1. Restock 2 cartons -> +24 packs -> 44 packs
	prod, err := s.AdjustStockPackaging(ctx, "a", "amilo", 2, models.UnitCarton, false)
	if err != nil {
		t.Fatalf("AdjustStockPackaging restock cartons failed: %v", err)
	}
	if prod.Quantity != 44 {
		t.Fatalf("expected 44 packs after 2 carton restock, got %d", prod.Quantity)
	}
	stock(t, s, 44)

	// 2. Sale 1 carton -> -12 packs -> 32 packs
	prod, err = s.AdjustStockPackaging(ctx, "a", "amilo", 1, models.UnitCarton, true)
	if err != nil {
		t.Fatalf("AdjustStockPackaging sale carton failed: %v", err)
	}
	if prod.Quantity != 32 {
		t.Fatalf("expected 32 packs after 1 carton sale, got %d", prod.Quantity)
	}
	stock(t, s, 32)

	// 3. Restock 5 base units (UnitPack) directly -> +5 packs -> 37 packs
	prod, err = s.AdjustStockPackaging(ctx, "a", "amilo", 5, models.UnitPack, false)
	if err != nil {
		t.Fatalf("AdjustStockPackaging base unit failed: %v", err)
	}
	if prod.Quantity != 37 {
		t.Fatalf("expected 37 packs, got %d", prod.Quantity)
	}
	stock(t, s, 37)

	// 4. Empty unit defaults to base unit -> -3 packs -> 34 packs
	prod, err = s.AdjustStockPackaging(ctx, "a", "amilo", 3, "", true)
	if err != nil {
		t.Fatalf("AdjustStockPackaging empty unit failed: %v", err)
	}
	if prod.Quantity != 34 {
		t.Fatalf("expected 34 packs, got %d", prod.Quantity)
	}
	stock(t, s, 34)

	// 5. Zero quantity is a no-op
	prod, err = s.AdjustStockPackaging(ctx, "a", "amilo", 0, models.UnitCarton, false)
	if err != nil {
		t.Fatalf("AdjustStockPackaging zero qty failed: %v", err)
	}
	if prod.Quantity != 34 {
		t.Fatalf("expected 34 packs unchanged, got %d", prod.Quantity)
	}

	// 6. Insufficient stock refusal: sell 3 cartons (36 packs) when only 34 available
	_, err = s.AdjustStockPackaging(ctx, "a", "amilo", 3, models.UnitCarton, true)
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	// 7. Unsupported unit conversion fails
	_, err = s.AdjustStockPackaging(ctx, "a", "amilo", 2, models.UnitBottle, false)
	if err == nil {
		t.Fatalf("expected error for unsupported packaging unit conversion, got nil")
	}

	// 8. Cross-shop isolation: shop "b" cannot adjust shop "a"'s product
	_, err = s.AdjustStockPackaging(ctx, "b", "amilo", 1, models.UnitCarton, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-shop adjustment, got %v", err)
	}
}
