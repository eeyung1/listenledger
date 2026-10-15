package models

import (
	"strings"
	"testing"
	"time"
)

func TestExpenseCategory_Valid(t *testing.T) {
	validCategories := []ExpenseCategory{
		ExpenseCategoryGeneratorFuel,
		ExpenseCategoryStoreRent,
		ExpenseCategoryTransportLogistics,
		ExpenseCategoryPackagingSupplies,
		ExpenseCategoryStaffWages,
		ExpenseCategoryUtilities,
		ExpenseCategoryRepairs,
		ExpenseCategoryOther,
	}

	for _, c := range validCategories {
		if !c.Valid() {
			t.Errorf("expected category %s to be valid", c)
		}
		if c.DisplayName() == "" {
			t.Errorf("expected display name for %s", c)
		}
	}

	invalid := ExpenseCategory("invalid_category")
	if invalid.Valid() {
		t.Errorf("expected invalid category to return false")
	}
}

func TestExpense_Validate(t *testing.T) {
	valid := Expense{
		ID:          "exp_01j7abc123",
		ShopID:      "shop_01",
		Category:    ExpenseCategoryGeneratorFuel,
		Amount:      Money{Amount: 450000, Currency: "NGN"},
		Description: "10L fuel for counter generator",
		ReceiptRef:  "REC-9821",
		OccurredAt:  time.Now().UTC().Add(-1 * time.Hour),
		CreatedBy:   "owner",
		CreatedAt:   time.Now().UTC(),
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid expense, got err: %v", err)
	}

	tests := []struct {
		name    string
		modify  func(e *Expense)
		wantErr string
	}{
		{
			name: "missing id",
			modify: func(e *Expense) {
				e.ID = ""
			},
			wantErr: "expense id is required",
		},
		{
			name: "missing shop_id",
			modify: func(e *Expense) {
				e.ShopID = "   "
			},
			wantErr: "expense shop_id is required",
		},
		{
			name: "invalid category",
			modify: func(e *Expense) {
				e.Category = "junk"
			},
			wantErr: "invalid expense category",
		},
		{
			name: "zero or negative amount",
			modify: func(e *Expense) {
				e.Amount.Amount = 0
			},
			wantErr: "expense amount must be greater than zero",
		},
		{
			name: "invalid currency code",
			modify: func(e *Expense) {
				e.Amount.Currency = "US"
			},
			wantErr: "expense currency must be a 3-letter code",
		},
		{
			name: "empty description",
			modify: func(e *Expense) {
				e.Description = "   "
			},
			wantErr: "expense description is required",
		},
		{
			name: "excessive description length",
			modify: func(e *Expense) {
				e.Description = strings.Repeat("A", 501)
			},
			wantErr: "expense description exceeds 500 characters",
		},
		{
			name: "zero occurred_at",
			modify: func(e *Expense) {
				e.OccurredAt = time.Time{}
			},
			wantErr: "expense occurred_at timestamp is required",
		},
		{
			name: "future occurred_at exceeding 24 hours",
			modify: func(e *Expense) {
				e.OccurredAt = time.Now().Add(48 * time.Hour)
			},
			wantErr: "expense occurred_at cannot be more than 24 hours in the future",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copy := valid
			tt.modify(&copy)
			err := copy.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error %q to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}
