package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

func setupCustomerTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	shopID := "shop_cust_test_01"
	err = store.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  "owner_cust_01",
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

func TestCustomers_LifecycleAndDebtMovements(t *testing.T) {
	s, shopID := setupCustomerTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)

	// 1. Create Customer
	cust1 := models.Customer{
		ID:                 "cus_test_01",
		ShopID:             shopID,
		Name:               "Alhaji Musa",
		Phone:              "+2348030001111",
		Notes:              "Pays monthly",
		OutstandingBalance: models.Money{Amount: 0, Currency: "NGN"},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.CreateCustomer(ctx, cust1); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	cust2 := models.Customer{
		ID:                 "cus_test_02",
		ShopID:             shopID,
		Name:               "Mama Chinedu",
		Phone:              "+2348030002222",
		Notes:              "Weekly bread & eggs credit",
		OutstandingBalance: models.Money{Amount: 0, Currency: "NGN"},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.CreateCustomer(ctx, cust2); err != nil {
		t.Fatalf("create customer 2: %v", err)
	}

	// 2. Fetch Customer by ID
	got, err := s.GetCustomerByID(ctx, shopID, cust1.ID)
	if err != nil {
		t.Fatalf("get customer by id: %v", err)
	}
	if got.Name != cust1.Name || got.OutstandingBalance.Amount != 0 {
		t.Errorf("unexpected customer data: %+v", got)
	}

	// 3. Update Customer
	got.Notes = "Updated notes: pays promptly"
	if err := s.UpdateCustomer(ctx, shopID, got); err != nil {
		t.Fatalf("update customer: %v", err)
	}
	updated, err := s.GetCustomerByID(ctx, shopID, cust1.ID)
	if err != nil {
		t.Fatalf("re-fetch customer: %v", err)
	}
	if updated.Notes != "Updated notes: pays promptly" {
		t.Errorf("expected updated notes, got %q", updated.Notes)
	}

	// 4. Record Debt Entry: Credit Purchase
	debt1 := models.CustomerDebt{
		ID:          "dbt_test_01",
		ShopID:      shopID,
		CustomerID:  cust1.ID,
		Type:        models.DebtTypeCreditSale,
		Amount:      models.Money{Amount: 450000, Currency: "NGN"},
		Description: "2 cartons of Indomie noodles",
		ReceiptRef:  "rcpt_ind_01",
		OccurredAt:  now,
		CreatedBy:   "owner",
	}
	recordedDebt1, err := s.RecordCustomerDebt(ctx, debt1)
	if err != nil {
		t.Fatalf("record credit sale: %v", err)
	}
	if recordedDebt1.BalanceAfter.Amount != 450000 {
		t.Errorf("expected balance after 450000, got %d", recordedDebt1.BalanceAfter.Amount)
	}

	// Verify customer balance updated
	cust1Refetched, _ := s.GetCustomerByID(ctx, shopID, cust1.ID)
	if cust1Refetched.OutstandingBalance.Amount != 450000 {
		t.Errorf("expected customer balance 450000, got %d", cust1Refetched.OutstandingBalance.Amount)
	}

	// 5. Record Another Credit Purchase
	debt2 := models.CustomerDebt{
		ID:          "dbt_test_02",
		ShopID:      shopID,
		CustomerID:  cust1.ID,
		Type:        models.DebtTypeCreditSale,
		Amount:      models.Money{Amount: 150000, Currency: "NGN"},
		Description: "1 tin of Milo",
		ReceiptRef:  "rcpt_milo_01",
		OccurredAt:  now.Add(1 * time.Minute),
		CreatedBy:   "cashier",
	}
	recordedDebt2, err := s.RecordCustomerDebt(ctx, debt2)
	if err != nil {
		t.Fatalf("record credit sale 2: %v", err)
	}
	if recordedDebt2.BalanceAfter.Amount != 600000 {
		t.Errorf("expected balance after 600000, got %d", recordedDebt2.BalanceAfter.Amount)
	}

	// 6. Record Partial Repayment
	debtRepayment := models.CustomerDebt{
		ID:          "dbt_test_03",
		ShopID:      shopID,
		CustomerID:  cust1.ID,
		Type:        models.DebtTypeRepayment,
		Amount:      models.Money{Amount: 200000, Currency: "NGN"},
		Description: "Cash repayment brought to shop",
		OccurredAt:  now.Add(2 * time.Minute),
		CreatedBy:   "owner",
	}
	recordedRepay, err := s.RecordCustomerDebt(ctx, debtRepayment)
	if err != nil {
		t.Fatalf("record repayment: %v", err)
	}
	if recordedRepay.BalanceAfter.Amount != 400000 {
		t.Errorf("expected balance after repayment 400000, got %d", recordedRepay.BalanceAfter.Amount)
	}

	// 7. Record Debt Forgiveness / Discount
	debtForgive := models.CustomerDebt{
		ID:          "dbt_test_04",
		ShopID:      shopID,
		CustomerID:  cust1.ID,
		Type:        models.DebtTypeForgiveness,
		Amount:      models.Money{Amount: 50000, Currency: "NGN"},
		Description: "Rounding discount / goodwill",
		OccurredAt:  now.Add(3 * time.Minute),
		CreatedBy:   "owner",
	}
	recordedForgive, err := s.RecordCustomerDebt(ctx, debtForgive)
	if err != nil {
		t.Fatalf("record forgiveness: %v", err)
	}
	if recordedForgive.BalanceAfter.Amount != 350000 {
		t.Errorf("expected balance after forgiveness 350000, got %d", recordedForgive.BalanceAfter.Amount)
	}

	// 8. List Customer Debts
	debts, totalDebts, err := s.ListCustomerDebts(ctx, shopID, cust1.ID, 10, 0)
	if err != nil {
		t.Fatalf("list customer debts: %v", err)
	}
	if totalDebts != 4 || len(debts) != 4 {
		t.Errorf("expected 4 debts, got total=%d len=%d", totalDebts, len(debts))
	}
	// Ordered newest first
	if debts[0].ID != "dbt_test_04" {
		t.Errorf("expected newest debt first (dbt_test_04), got %s", debts[0].ID)
	}

	// 9. List Customers with Search and Debt Filter
	// cust1 has 350000, cust2 has 0
	listDebtOnly, countDebtOnly, err := s.ListCustomers(ctx, CustomerQuery{
		ShopID:      shopID,
		HasDebtOnly: true,
	})
	if err != nil {
		t.Fatalf("list customers debt only: %v", err)
	}
	if countDebtOnly != 1 || len(listDebtOnly) != 1 || listDebtOnly[0].ID != cust1.ID {
		t.Errorf("expected only cust1 in debt list, got count=%d len=%d", countDebtOnly, len(listDebtOnly))
	}

	// Search by name
	listSearch, countSearch, err := s.ListCustomers(ctx, CustomerQuery{
		ShopID: shopID,
		Search: "Chinedu",
	})
	if err != nil {
		t.Fatalf("list customers search: %v", err)
	}
	if countSearch != 1 || len(listSearch) != 1 || listSearch[0].ID != cust2.ID {
		t.Errorf("expected only cust2 in search, got count=%d", countSearch)
	}

	// 10. Shop Credit Summary
	summary, err := s.GetShopCreditSummary(ctx, shopID, now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("get shop credit summary: %v", err)
	}
	if summary.TotalCustomersWithDebt != 1 {
		t.Errorf("expected 1 customer with debt, got %d", summary.TotalCustomersWithDebt)
	}
	if summary.TotalOutstandingDebt.Amount != 350000 {
		t.Errorf("expected total debt 350000, got %d", summary.TotalOutstandingDebt.Amount)
	}
	if summary.TodayCreditSalesAmount.Amount != 600000 { // 450000 + 150000
		t.Errorf("expected today credit sales 600000, got %d", summary.TodayCreditSalesAmount.Amount)
	}
	if summary.TodayRepaymentsAmount.Amount != 200000 {
		t.Errorf("expected today repayments 200000, got %d", summary.TodayRepaymentsAmount.Amount)
	}
}

func TestCustomers_CrossShopIsolation(t *testing.T) {
	s, shopA := setupCustomerTestStore(t)
	ctx := context.Background()

	shopB := "shop_cust_b"
	err := s.CreateAccount(ctx, Account{
		Identity: Identity{
			OwnerID:  "owner_cust_b",
			Email:    "b@example.com",
			ShopID:   shopB,
			ShopName: "Shop B",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "passB",
		RecoveryHash: "recB",
	})
	if err != nil {
		t.Fatalf("create account B failed: %v", err)
	}

	now := time.Now().UTC()

	c := models.Customer{
		ID:                 "cus_iso_a",
		ShopID:             shopA,
		Name:               "Secret Customer Shop A",
		OutstandingBalance: models.Money{Amount: 100000, Currency: "NGN"},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.CreateCustomer(ctx, c); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	// Shop B cannot get Shop A's customer
	_, err = s.GetCustomerByID(ctx, shopB, c.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound across tenant boundary, got %v", err)
	}

	// Shop B cannot update Shop A's customer
	c.Name = "Hijacked Name"
	err = s.UpdateCustomer(ctx, shopB, c)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on update across tenants, got %v", err)
	}

	// Shop B cannot record debt on Shop A's customer
	_, err = s.RecordCustomerDebt(ctx, models.CustomerDebt{
		ID:          "dbt_iso_fail",
		ShopID:      shopB,
		CustomerID:  c.ID,
		Type:        models.DebtTypeCreditSale,
		Amount:      models.Money{Amount: 50000, Currency: "NGN"},
		Description: "Spoofed cross-shop debt",
		OccurredAt:  now,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on cross-shop debt insertion, got %v", err)
	}
}
