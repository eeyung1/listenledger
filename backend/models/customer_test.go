package models

import (
	"strings"
	"testing"
	"time"
)

func TestCustomer_Validate(t *testing.T) {
	now := time.Now().UTC()

	validCustomer := Customer{
		ID:                 "cus_01JABC1234567890",
		ShopID:             "shop_01JXYZ",
		Name:               "Ibrahim Aliko",
		Phone:              "+2348031234567",
		Notes:              "Pays every Friday after Juma'at",
		OutstandingBalance: Money{Amount: 500000, Currency: "NGN"},
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := validCustomer.Validate(); err != nil {
		t.Fatalf("expected valid customer, got err: %v", err)
	}

	tests := []struct {
		name    string
		modify  func(*Customer)
		wantErr string
	}{
		{
			name:    "empty id",
			modify:  func(c *Customer) { c.ID = "   " },
			wantErr: "customer id is required",
		},
		{
			name:    "empty shop id",
			modify:  func(c *Customer) { c.ShopID = "" },
			wantErr: "customer shop_id is required",
		},
		{
			name:    "empty name",
			modify:  func(c *Customer) { c.Name = "  " },
			wantErr: "customer name is required",
		},
		{
			name:    "name exceeds 150 chars",
			modify:  func(c *Customer) { c.Name = strings.Repeat("A", 151) },
			wantErr: "customer name exceeds 150 characters",
		},
		{
			name:    "phone exceeds 32 chars",
			modify:  func(c *Customer) { c.Phone = strings.Repeat("1", 33) },
			wantErr: "customer phone exceeds 32 characters",
		},
		{
			name:    "notes exceeds 500 chars",
			modify:  func(c *Customer) { c.Notes = strings.Repeat("N", 501) },
			wantErr: "customer notes exceeds 500 characters",
		},
		{
			name:    "invalid currency",
			modify:  func(c *Customer) { c.OutstandingBalance.Currency = "US" },
			wantErr: "customer balance currency must be a 3-letter code",
		},
		{
			name:    "zero created_at",
			modify:  func(c *Customer) { c.CreatedAt = time.Time{} },
			wantErr: "customer created_at timestamp is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validCustomer
			tc.modify(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestCustomerDebt_Validate(t *testing.T) {
	now := time.Now().UTC()

	validDebt := CustomerDebt{
		ID:           "dbt_01JABC1234567890",
		ShopID:       "shop_01JXYZ",
		CustomerID:   "cus_01JABC1234567890",
		Type:         DebtTypeCreditSale,
		Amount:       Money{Amount: 250000, Currency: "NGN"},
		BalanceAfter: Money{Amount: 750000, Currency: "NGN"},
		Description:  "2 packs of Peak milk and sugar",
		ReceiptRef:   "rcpt_01JREF",
		OccurredAt:   now,
		CreatedBy:    "cashier",
		CreatedAt:    now,
	}

	if err := validDebt.Validate(); err != nil {
		t.Fatalf("expected valid debt, got err: %v", err)
	}

	tests := []struct {
		name    string
		modify  func(*CustomerDebt)
		wantErr string
	}{
		{
			name:    "empty id",
			modify:  func(d *CustomerDebt) { d.ID = "" },
			wantErr: "customer debt id is required",
		},
		{
			name:    "empty shop id",
			modify:  func(d *CustomerDebt) { d.ShopID = "" },
			wantErr: "customer debt shop_id is required",
		},
		{
			name:    "empty customer id",
			modify:  func(d *CustomerDebt) { d.CustomerID = "" },
			wantErr: "customer debt customer_id is required",
		},
		{
			name:    "invalid type",
			modify:  func(d *CustomerDebt) { d.Type = "unknown_type" },
			wantErr: "invalid customer debt type",
		},
		{
			name:    "zero or negative amount",
			modify:  func(d *CustomerDebt) { d.Amount.Amount = 0 },
			wantErr: "debt amount must be greater than zero",
		},
		{
			name:    "invalid amount currency",
			modify:  func(d *CustomerDebt) { d.Amount.Currency = "N" },
			wantErr: "debt currency must be a 3-letter code",
		},
		{
			name:    "invalid balance_after currency",
			modify:  func(d *CustomerDebt) { d.BalanceAfter.Currency = "" },
			wantErr: "balance_after currency must be a 3-letter code",
		},
		{
			name:    "empty description",
			modify:  func(d *CustomerDebt) { d.Description = "   " },
			wantErr: "debt description is required",
		},
		{
			name:    "description exceeds 500 chars",
			modify:  func(d *CustomerDebt) { d.Description = strings.Repeat("D", 501) },
			wantErr: "debt description exceeds 500 characters",
		},
		{
			name:    "receipt_ref exceeds 100 chars",
			modify:  func(d *CustomerDebt) { d.ReceiptRef = strings.Repeat("R", 101) },
			wantErr: "receipt reference exceeds 100 characters",
		},
		{
			name:    "zero occurred_at",
			modify:  func(d *CustomerDebt) { d.OccurredAt = time.Time{} },
			wantErr: "debt occurred_at timestamp is required",
		},
		{
			name:    "occurred_at too far in future",
			modify:  func(d *CustomerDebt) { d.OccurredAt = time.Now().Add(48 * time.Hour) },
			wantErr: "debt occurred_at cannot be more than 24 hours in the future",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := validDebt
			tc.modify(&d)
			err := d.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestCustomerDebtType_DisplayName(t *testing.T) {
	if DebtTypeCreditSale.DisplayName() != "Credit Purchase" {
		t.Errorf("unexpected display name: %s", DebtTypeCreditSale.DisplayName())
	}
	if DebtTypeRepayment.DisplayName() != "Debt Repayment" {
		t.Errorf("unexpected display name: %s", DebtTypeRepayment.DisplayName())
	}
	if DebtTypeForgiveness.DisplayName() != "Debt Forgiveness / Write-off" {
		t.Errorf("unexpected display name: %s", DebtTypeForgiveness.DisplayName())
	}
}
