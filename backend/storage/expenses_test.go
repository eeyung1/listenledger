package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

func setupTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	shopID := "shop_exp_test_01"
	err = store.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  "owner_exp_01",
			Email:    "test@example.com",
			ShopID:   shopID,
			ShopName: "Mama Tolu Provisions",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "pass",
		RecoveryHash: "rec",
	})
	if err != nil {
		t.Fatalf("create account failed: %v", err)
	}

	return store, shopID
}

func TestStore_CreateAndGetExpense(t *testing.T) {
	store, shopID := setupTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	exp := models.Expense{
		ID:          "exp_fuel_001",
		ShopID:      shopID,
		Category:    models.ExpenseCategoryGeneratorFuel,
		Amount:      models.Money{Amount: 500000, Currency: "NGN"}, // 5,000 NGN
		Description: "10 litres petrol for shop generator",
		ReceiptRef:  "REC-FUEL-99",
		OccurredAt:  now,
		CreatedBy:   "owner",
		CreatedAt:   now,
	}

	if err := store.CreateExpense(ctx, exp); err != nil {
		t.Fatalf("create expense failed: %v", err)
	}

	got, err := store.GetExpenseByID(ctx, shopID, exp.ID)
	if err != nil {
		t.Fatalf("get expense failed: %v", err)
	}

	if got.ID != exp.ID {
		t.Errorf("expected ID %s, got %s", exp.ID, got.ID)
	}
	if got.ShopID != exp.ShopID {
		t.Errorf("expected ShopID %s, got %s", exp.ShopID, got.ShopID)
	}
	if got.Category != exp.Category {
		t.Errorf("expected Category %s, got %s", exp.Category, got.Category)
	}
	if got.Amount.Amount != exp.Amount.Amount || got.Amount.Currency != exp.Amount.Currency {
		t.Errorf("expected Amount %+v, got %+v", exp.Amount, got.Amount)
	}
	if got.Description != exp.Description {
		t.Errorf("expected Description %s, got %s", exp.Description, got.Description)
	}
	if got.ReceiptRef != exp.ReceiptRef {
		t.Errorf("expected ReceiptRef %s, got %s", exp.ReceiptRef, got.ReceiptRef)
	}
	if !got.OccurredAt.Equal(exp.OccurredAt) {
		t.Errorf("expected OccurredAt %v, got %v", exp.OccurredAt, got.OccurredAt)
	}
	if got.CreatedBy != exp.CreatedBy {
		t.Errorf("expected CreatedBy %s, got %s", exp.CreatedBy, got.CreatedBy)
	}
}

func TestStore_ListExpenses_FiltersAndPagination(t *testing.T) {
	store, shopID := setupTestStore(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)

	expenses := []models.Expense{
		{
			ID:          "exp_1",
			ShopID:      shopID,
			Category:    models.ExpenseCategoryGeneratorFuel,
			Amount:      models.Money{Amount: 400000, Currency: "NGN"},
			Description: "Fuel",
			OccurredAt:  baseTime.Add(1 * time.Hour),
			CreatedBy:   "owner",
			CreatedAt:   baseTime.Add(1 * time.Hour),
		},
		{
			ID:          "exp_2",
			ShopID:      shopID,
			Category:    models.ExpenseCategoryTransportLogistics,
			Amount:      models.Money{Amount: 150000, Currency: "NGN"},
			Description: "Keke delivery for cartons",
			OccurredAt:  baseTime.Add(2 * time.Hour),
			CreatedBy:   "owner",
			CreatedAt:   baseTime.Add(2 * time.Hour),
		},
		{
			ID:          "exp_3",
			ShopID:      shopID,
			Category:    models.ExpenseCategoryGeneratorFuel,
			Amount:      models.Money{Amount: 600000, Currency: "NGN"},
			Description: "Evening fuel refill",
			OccurredAt:  baseTime.Add(3 * time.Hour),
			CreatedBy:   "cashier",
			CreatedAt:   baseTime.Add(3 * time.Hour),
		},
		{
			ID:          "exp_4",
			ShopID:      shopID,
			Category:    models.ExpenseCategoryPackagingSupplies,
			Amount:      models.Money{Amount: 200000, Currency: "NGN"},
			Description: "Nylon carrier bags roll",
			OccurredAt:  baseTime.Add(4 * time.Hour),
			CreatedBy:   "owner",
			CreatedAt:   baseTime.Add(4 * time.Hour),
		},
	}

	for _, e := range expenses {
		if err := store.CreateExpense(ctx, e); err != nil {
			t.Fatalf("create expense %s: %v", e.ID, err)
		}
	}

	// 1. Unfiltered list
	list, total, err := store.ListExpenses(ctx, ExpenseQuery{ShopID: shopID})
	if err != nil {
		t.Fatalf("list expenses unfiltered failed: %v", err)
	}
	if total != 4 || len(list) != 4 {
		t.Errorf("expected 4 total expenses, got total=%d len=%d", total, len(list))
	}
	// Verify descending order
	if list[0].ID != "exp_4" || list[3].ID != "exp_1" {
		t.Errorf("expected newest first order, got %s ... %s", list[0].ID, list[3].ID)
	}

	// 2. Filter by Category
	fuelList, fuelTotal, err := store.ListExpenses(ctx, ExpenseQuery{
		ShopID:   shopID,
		Category: models.ExpenseCategoryGeneratorFuel,
	})
	if err != nil {
		t.Fatalf("list fuel expenses failed: %v", err)
	}
	if fuelTotal != 2 || len(fuelList) != 2 {
		t.Errorf("expected 2 fuel expenses, got total=%d len=%d", fuelTotal, len(fuelList))
	}

	// 3. Filter by Date range
	from := baseTime.Add(90 * time.Minute)
	to := baseTime.Add(210 * time.Minute)
	dateList, dateTotal, err := store.ListExpenses(ctx, ExpenseQuery{
		ShopID: shopID,
		From:   &from,
		To:     &to,
	})
	if err != nil {
		t.Fatalf("list date-filtered expenses failed: %v", err)
	}
	if dateTotal != 2 || len(dateList) != 2 {
		t.Errorf("expected 2 expenses in date window, got total=%d len=%d", dateTotal, len(dateList))
	}

	// 4. Pagination (Limit 2, Offset 1)
	pagedList, pagedTotal, err := store.ListExpenses(ctx, ExpenseQuery{
		ShopID: shopID,
		Limit:  2,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("list paged expenses failed: %v", err)
	}
	if pagedTotal != 4 {
		t.Errorf("expected total 4, got %d", pagedTotal)
	}
	if len(pagedList) != 2 {
		t.Errorf("expected 2 items on page, got %d", len(pagedList))
	}
	if pagedList[0].ID != "exp_3" || pagedList[1].ID != "exp_2" {
		t.Errorf("unexpected page items: %s, %s", pagedList[0].ID, pagedList[1].ID)
	}
}

func TestStore_DeleteExpense(t *testing.T) {
	store, shopID := setupTestStore(t)
	ctx := context.Background()

	exp := models.Expense{
		ID:          "exp_to_delete",
		ShopID:      shopID,
		Category:    models.ExpenseCategoryOther,
		Amount:      models.Money{Amount: 100000, Currency: "NGN"},
		Description: "Misc fee",
		OccurredAt:  time.Now().UTC(),
		CreatedBy:   "owner",
		CreatedAt:   time.Now().UTC(),
	}

	if err := store.CreateExpense(ctx, exp); err != nil {
		t.Fatalf("create expense: %v", err)
	}

	if err := store.DeleteExpense(ctx, shopID, exp.ID); err != nil {
		t.Fatalf("delete expense failed: %v", err)
	}

	// Fetching deleted expense should return ErrNotFound
	_, err := store.GetExpenseByID(ctx, shopID, exp.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for deleted expense, got %v", err)
	}

	// Deleting again should return ErrNotFound
	if err := store.DeleteExpense(ctx, shopID, exp.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound deleting non-existent expense, got %v", err)
	}
}

func TestStore_CrossShopIsolation_Expenses(t *testing.T) {
	store, shopA := setupTestStore(t)
	ctx := context.Background()

	shopB := "shop_exp_test_02"
	err := store.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  "owner_exp_02",
			Email:    "shopb@example.com",
			ShopID:   shopB,
			ShopName: "Emeka Supermarket",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "pass",
		RecoveryHash: "rec",
	})
	if err != nil {
		t.Fatalf("create account for shop B: %v", err)
	}

	expA := models.Expense{
		ID:          "exp_shop_a",
		ShopID:      shopA,
		Category:    models.ExpenseCategoryGeneratorFuel,
		Amount:      models.Money{Amount: 300000, Currency: "NGN"},
		Description: "Shop A generator petrol",
		OccurredAt:  time.Now().UTC(),
		CreatedBy:   "owner_a",
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.CreateExpense(ctx, expA); err != nil {
		t.Fatalf("create expense for shop A: %v", err)
	}

	// Shop B cannot read Shop A's expense
	if _, err := store.GetExpenseByID(ctx, shopB, expA.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound reading Shop A's expense from Shop B, got %v", err)
	}

	// Shop B cannot delete Shop A's expense
	if err := store.DeleteExpense(ctx, shopB, expA.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound deleting Shop A's expense from Shop B, got %v", err)
	}

	// Shop B list does not show Shop A's expense
	listB, totalB, err := store.ListExpenses(ctx, ExpenseQuery{ShopID: shopB})
	if err != nil {
		t.Fatalf("list expenses for shop B: %v", err)
	}
	if totalB != 0 || len(listB) != 0 {
		t.Errorf("expected 0 expenses for Shop B, got total=%d len=%d", totalB, len(listB))
	}
}

func TestStore_GetExpenseSummary(t *testing.T) {
	store, shopID := setupTestStore(t)
	ctx := context.Background()

	today := time.Now().UTC().Truncate(24 * time.Hour)

	expenses := []models.Expense{
		{
			ID:          "exp_sum_1",
			ShopID:      shopID,
			Category:    models.ExpenseCategoryGeneratorFuel,
			Amount:      models.Money{Amount: 500000, Currency: "NGN"},
			Description: "Fuel 1",
			OccurredAt:  today.Add(2 * time.Hour),
			CreatedBy:   "owner",
			CreatedAt:   today.Add(2 * time.Hour),
		},
		{
			ID:          "exp_sum_2",
			ShopID:      shopID,
			Category:    models.ExpenseCategoryGeneratorFuel,
			Amount:      models.Money{Amount: 300000, Currency: "NGN"},
			Description: "Fuel 2",
			OccurredAt:  today.Add(4 * time.Hour),
			CreatedBy:   "owner",
			CreatedAt:   today.Add(4 * time.Hour),
		},
		{
			ID:          "exp_sum_3",
			ShopID:      shopID,
			Category:    models.ExpenseCategoryTransportLogistics,
			Amount:      models.Money{Amount: 200000, Currency: "NGN"},
			Description: "Transport",
			OccurredAt:  today.Add(5 * time.Hour),
			CreatedBy:   "owner",
			CreatedAt:   today.Add(5 * time.Hour),
		},
	}

	for _, e := range expenses {
		if err := store.CreateExpense(ctx, e); err != nil {
			t.Fatalf("create expense: %v", err)
		}
	}

	summary, err := store.GetExpenseSummary(ctx, shopID, today, today.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("get expense summary failed: %v", err)
	}

	if summary.TotalCount != 3 {
		t.Errorf("expected TotalCount 3, got %d", summary.TotalCount)
	}
	if summary.TotalAmount.Amount != 1000000 { // 5000 + 3000 + 2000 = 10,000 NGN = 1,000,000 kobo
		t.Errorf("expected TotalAmount 1000000, got %d", summary.TotalAmount.Amount)
	}
	if summary.ByCategory[models.ExpenseCategoryGeneratorFuel] != 800000 {
		t.Errorf("expected fuel sum 800000, got %d", summary.ByCategory[models.ExpenseCategoryGeneratorFuel])
	}
	if summary.ByCategory[models.ExpenseCategoryTransportLogistics] != 200000 {
		t.Errorf("expected transport sum 200000, got %d", summary.ByCategory[models.ExpenseCategoryTransportLogistics])
	}
}

func TestStore_DailySummary_WithExpensesAndNetProfit(t *testing.T) {
	store, shopID := setupTestStore(t)
	ctx := context.Background()

	today := time.Now().UTC().Truncate(24 * time.Hour)

	// 1. Product & Sale (Revenue: 25,000 NGN = 2,500,000 kobo)
	prod := models.Product{
		ID:                "prd_milo_daily",
		ShopID:            shopID,
		Name:              "Milo Tin",
		SKU:               "MILO-TIN",
		Price:             models.Money{Amount: 2500000, Currency: "NGN"},
		Quantity:          50,
		BaseUnit:          models.UnitPiece,
		LowStockThreshold: 5,
	}
	if err := store.InsertProduct(ctx, prod); err != nil {
		t.Fatalf("insert product: %v", err)
	}

	sale := models.Transaction{
		ID:          "txn_sale_daily_01",
		ShopID:      shopID,
		Type:        models.TypeSale,
		ProductID:   prod.ID,
		ProductName: prod.Name,
		Quantity:    1,
		Unit:        models.UnitPiece,
		Amount:      models.Money{Amount: 2500000, Currency: "NGN"},
		Confidence:  models.Confidence{Overall: 0.98},
		Status:      models.StatusConfirmed,
		OccurredAt:  today.Add(3 * time.Hour),
		Transcript:  "One Milo tin, twenty five thousand naira. Thank you.",
	}
	if _, err := store.RecordMovement(ctx, sale, "idem_sale_daily", "hash_sale_daily"); err != nil {
		t.Fatalf("record sale: %v", err)
	}

	// 2. Operational Expense (Fuel: 6,000 NGN = 600,000 kobo)
	exp := models.Expense{
		ID:          "exp_daily_01",
		ShopID:      shopID,
		Category:    models.ExpenseCategoryGeneratorFuel,
		Amount:      models.Money{Amount: 600000, Currency: "NGN"},
		Description: "Generator petrol for the day",
		OccurredAt:  today.Add(4 * time.Hour),
		CreatedBy:   "owner",
		CreatedAt:   today.Add(4 * time.Hour),
	}
	if err := store.CreateExpense(ctx, exp); err != nil {
		t.Fatalf("create expense: %v", err)
	}

	// 3. Compute Daily Summary
	summary, err := store.GetDailySummary(ctx, shopID, today, today.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("get daily summary failed: %v", err)
	}

	if summary.TodaySalesAmount.Amount != 2500000 {
		t.Errorf("expected today_sales_amount 2500000, got %d", summary.TodaySalesAmount.Amount)
	}
	if summary.TodaySalesCount != 1 {
		t.Errorf("expected today_sales_count 1, got %d", summary.TodaySalesCount)
	}
	if summary.TodayExpensesAmount.Amount != 600000 {
		t.Errorf("expected today_expenses_amount 600000, got %d", summary.TodayExpensesAmount.Amount)
	}
	if summary.TodayExpensesCount != 1 {
		t.Errorf("expected today_expenses_count 1, got %d", summary.TodayExpensesCount)
	}
	// Net Profit = 2,500,000 - 600,000 = 1,900,000 kobo (19,000 NGN)
	expectedNetProfit := int64(1900000)
	if summary.TodayNetProfit.Amount != expectedNetProfit {
		t.Errorf("expected today_net_profit %d, got %d", expectedNetProfit, summary.TodayNetProfit.Amount)
	}
}
