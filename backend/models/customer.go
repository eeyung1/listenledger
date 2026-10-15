package models

import (
	"errors"
	"strings"
	"time"
)

// Customer represents a retail customer account tracked in the shop's credit book.
type Customer struct {
	ID                 string    `json:"id"`
	ShopID             string    `json:"shop_id"`
	Name               string    `json:"name"`
	Phone              string    `json:"phone,omitempty"`
	Notes              string    `json:"notes,omitempty"`
	OutstandingBalance Money     `json:"outstanding_balance"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Validate checks that customer fields satisfy business invariants.
func (c Customer) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("customer id is required")
	}
	if strings.TrimSpace(c.ShopID) == "" {
		return errors.New("customer shop_id is required")
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return errors.New("customer name is required")
	}
	if len(name) > 150 {
		return errors.New("customer name exceeds 150 characters")
	}
	if len(c.Phone) > 32 {
		return errors.New("customer phone exceeds 32 characters")
	}
	if len(c.Notes) > 500 {
		return errors.New("customer notes exceeds 500 characters")
	}
	if len(c.OutstandingBalance.Currency) != 3 {
		return errors.New("customer balance currency must be a 3-letter code")
	}
	if c.CreatedAt.IsZero() {
		return errors.New("customer created_at timestamp is required")
	}
	return nil
}

// CustomerDebtType classifies credit ledger movements (purchases on credit vs. debt repayments).
type CustomerDebtType string

const (
	DebtTypeCreditSale  CustomerDebtType = "credit_sale"
	DebtTypeRepayment   CustomerDebtType = "repayment"
	DebtTypeForgiveness CustomerDebtType = "forgiveness"
)

// Valid reports whether t is a recognized debt transaction type.
func (t CustomerDebtType) Valid() bool {
	switch t {
	case DebtTypeCreditSale, DebtTypeRepayment, DebtTypeForgiveness:
		return true
	default:
		return false
	}
}

// DisplayName returns a human-readable title for the debt transaction type.
func (t CustomerDebtType) DisplayName() string {
	switch t {
	case DebtTypeCreditSale:
		return "Credit Purchase"
	case DebtTypeRepayment:
		return "Debt Repayment"
	case DebtTypeForgiveness:
		return "Debt Forgiveness / Write-off"
	default:
		return string(t)
	}
}

// CustomerDebt records an atomic credit increase or repayment in a customer's ledger.
type CustomerDebt struct {
	ID           string           `json:"id"`
	ShopID       string           `json:"shop_id"`
	CustomerID   string           `json:"customer_id"`
	Type         CustomerDebtType `json:"type"`
	Amount       Money            `json:"amount"`
	BalanceAfter Money            `json:"balance_after"`
	Description  string           `json:"description"`
	ReceiptRef   string           `json:"receipt_ref,omitempty"`
	OccurredAt   time.Time        `json:"occurred_at"`
	CreatedBy    string           `json:"created_by"`
	CreatedAt    time.Time        `json:"created_at"`
}

// Validate checks that a customer debt record satisfies business invariants.
func (d CustomerDebt) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("customer debt id is required")
	}
	if strings.TrimSpace(d.ShopID) == "" {
		return errors.New("customer debt shop_id is required")
	}
	if strings.TrimSpace(d.CustomerID) == "" {
		return errors.New("customer debt customer_id is required")
	}
	if !d.Type.Valid() {
		return errors.New("invalid customer debt type")
	}
	if d.Amount.Amount <= 0 {
		return errors.New("debt amount must be greater than zero")
	}
	if len(d.Amount.Currency) != 3 {
		return errors.New("debt currency must be a 3-letter code")
	}
	if len(d.BalanceAfter.Currency) != 3 {
		return errors.New("balance_after currency must be a 3-letter code")
	}
	desc := strings.TrimSpace(d.Description)
	if desc == "" {
		return errors.New("debt description is required")
	}
	if len(desc) > 500 {
		return errors.New("debt description exceeds 500 characters")
	}
	if len(d.ReceiptRef) > 100 {
		return errors.New("receipt reference exceeds 100 characters")
	}
	if d.OccurredAt.IsZero() {
		return errors.New("debt occurred_at timestamp is required")
	}
	if d.OccurredAt.After(time.Now().Add(24 * time.Hour)) {
		return errors.New("debt occurred_at cannot be more than 24 hours in the future")
	}
	return nil
}
