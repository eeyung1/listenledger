package models

import (
	"errors"
	"strings"
	"time"
)

// ExpenseCategory classifies operational costs incurred during daily retail shop trading.
type ExpenseCategory string

const (
	ExpenseCategoryGeneratorFuel      ExpenseCategory = "generator_fuel"
	ExpenseCategoryStoreRent          ExpenseCategory = "store_rent"
	ExpenseCategoryTransportLogistics ExpenseCategory = "transport_logistics"
	ExpenseCategoryPackagingSupplies  ExpenseCategory = "packaging_supplies"
	ExpenseCategoryStaffWages         ExpenseCategory = "staff_wages"
	ExpenseCategoryUtilities          ExpenseCategory = "utilities"
	ExpenseCategoryRepairs            ExpenseCategory = "repairs"
	ExpenseCategoryOther              ExpenseCategory = "other"
)

// Valid reports whether c is a supported expense category.
func (c ExpenseCategory) Valid() bool {
	switch c {
	case ExpenseCategoryGeneratorFuel,
		ExpenseCategoryStoreRent,
		ExpenseCategoryTransportLogistics,
		ExpenseCategoryPackagingSupplies,
		ExpenseCategoryStaffWages,
		ExpenseCategoryUtilities,
		ExpenseCategoryRepairs,
		ExpenseCategoryOther:
		return true
	default:
		return false
	}
}

// DisplayName returns a human-readable title for the expense category.
func (c ExpenseCategory) DisplayName() string {
	switch c {
	case ExpenseCategoryGeneratorFuel:
		return "Generator Fuel & Oil"
	case ExpenseCategoryStoreRent:
		return "Store Rent & Facility Dues"
	case ExpenseCategoryTransportLogistics:
		return "Transport & Logistics"
	case ExpenseCategoryPackagingSupplies:
		return "Packaging & Bags"
	case ExpenseCategoryStaffWages:
		return "Staff Daily Wages"
	case ExpenseCategoryUtilities:
		return "Power & Water Utilities"
	case ExpenseCategoryRepairs:
		return "Repairs & Maintenance"
	case ExpenseCategoryOther:
		return "Other Operational Cost"
	default:
		return string(c)
	}
}

// Expense represents an operational expense record for a shop.
type Expense struct {
	ID          string          `json:"id"`
	ShopID      string          `json:"shop_id"`
	Category    ExpenseCategory `json:"category"`
	Amount      Money           `json:"amount"`
	Description string          `json:"description"`
	ReceiptRef  string          `json:"receipt_ref"`
	OccurredAt  time.Time       `json:"occurred_at"`
	CreatedBy   string          `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Validate checks that an expense satisfies business invariants.
func (e Expense) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return errors.New("expense id is required")
	}
	if strings.TrimSpace(e.ShopID) == "" {
		return errors.New("expense shop_id is required")
	}
	if !e.Category.Valid() {
		return errors.New("invalid expense category")
	}
	if e.Amount.Amount <= 0 {
		return errors.New("expense amount must be greater than zero")
	}
	if len(e.Amount.Currency) != 3 {
		return errors.New("expense currency must be a 3-letter code")
	}
	if strings.TrimSpace(e.Description) == "" {
		return errors.New("expense description is required")
	}
	if len(e.Description) > 500 {
		return errors.New("expense description exceeds 500 characters")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("expense occurred_at timestamp is required")
	}
	if e.OccurredAt.After(time.Now().Add(24 * time.Hour)) {
		return errors.New("expense occurred_at cannot be more than 24 hours in the future")
	}
	return nil
}
