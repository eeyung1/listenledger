package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

// CustomerQuery specifies filters and pagination for customer directory lookup.
type CustomerQuery struct {
	ShopID      string
	Search      string
	HasDebtOnly bool
	Limit       int
	Offset      int
}

// ShopCreditSummary summarizes customer debt and accounts receivable for a shop.
type ShopCreditSummary struct {
	ShopID                 string       `json:"shop_id"`
	Currency               string       `json:"currency"`
	TotalCustomersWithDebt int          `json:"total_customers_with_debt"`
	TotalOutstandingDebt   models.Money `json:"total_outstanding_debt"`
	TodayCreditSalesAmount models.Money `json:"today_credit_sales_amount"`
	TodayRepaymentsAmount  models.Money `json:"today_repayments_amount"`
}

// CreateCustomer registers a new customer in the shop's credit book.
func (s *Store) CreateCustomer(ctx context.Context, c models.Customer) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("storage: validate customer: %w", err)
	}

	query := s.rebind(`
		INSERT INTO customers (
			id,
			shop_id,
			name,
			phone,
			notes,
			outstanding_amount,
			outstanding_currency,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)

	createdAt := c.CreatedAt.UTC().Format(time.RFC3339Nano)
	updatedAt := c.UpdatedAt.UTC().Format(time.RFC3339Nano)

	_, err := s.db.ExecContext(
		ctx,
		query,
		c.ID,
		c.ShopID,
		c.Name,
		c.Phone,
		c.Notes,
		c.OutstandingBalance.Amount,
		c.OutstandingBalance.Currency,
		createdAt,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("storage: create customer: %w", err)
	}
	return nil
}

// GetCustomerByID retrieves a customer by ID within a specific shop.
func (s *Store) GetCustomerByID(ctx context.Context, shopID, customerID string) (models.Customer, error) {
	if strings.TrimSpace(shopID) == "" || strings.TrimSpace(customerID) == "" {
		return models.Customer{}, errors.New("storage: shop_id and customer_id required")
	}

	query := s.rebind(`
		SELECT
			id,
			shop_id,
			name,
			phone,
			notes,
			outstanding_amount,
			outstanding_currency,
			created_at,
			updated_at
		FROM customers
		WHERE shop_id = ? AND id = ?
	`)

	var (
		c         models.Customer
		createdAt string
		updatedAt string
	)

	err := s.db.QueryRowContext(ctx, query, shopID, customerID).Scan(
		&c.ID,
		&c.ShopID,
		&c.Name,
		&c.Phone,
		&c.Notes,
		&c.OutstandingBalance.Amount,
		&c.OutstandingBalance.Currency,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Customer{}, ErrNotFound
	}
	if err != nil {
		return models.Customer{}, fmt.Errorf("storage: get customer: %w", err)
	}

	parsedCreated, err := time.Parse(time.RFC3339Nano, createdAt)
	if err == nil {
		c.CreatedAt = parsedCreated
	}
	parsedUpdated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err == nil {
		c.UpdatedAt = parsedUpdated
	}

	return c, nil
}

// ListCustomers queries customers with optional name/phone search and debt filters.
func (s *Store) ListCustomers(ctx context.Context, q CustomerQuery) ([]models.Customer, int, error) {
	if strings.TrimSpace(q.ShopID) == "" {
		return nil, 0, errors.New("storage: shop_id is required")
	}

	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	var where []string
	var args []any

	where = append(where, "shop_id = ?")
	args = append(args, q.ShopID)

	search := strings.TrimSpace(q.Search)
	if search != "" {
		where = append(where, "(name LIKE ? OR phone LIKE ?)")
		pattern := "%" + search + "%"
		args = append(args, pattern, pattern)
	}

	if q.HasDebtOnly {
		where = append(where, "outstanding_amount > 0")
	}

	whereClause := strings.Join(where, " AND ")

	countQuery := s.rebind(fmt.Sprintf("SELECT COUNT(*) FROM customers WHERE %s", whereClause))
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("storage: count customers: %w", err)
	}

	listArgs := append([]any{}, args...)
	listArgs = append(listArgs, limit, offset)

	listQuery := s.rebind(fmt.Sprintf(`
		SELECT
			id,
			shop_id,
			name,
			phone,
			notes,
			outstanding_amount,
			outstanding_currency,
			created_at,
			updated_at
		FROM customers
		WHERE %s
		ORDER BY outstanding_amount DESC, name ASC, id ASC
		LIMIT ? OFFSET ?
	`, whereClause))

	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: list customers: %w", err)
	}
	defer rows.Close()

	customers := make([]models.Customer, 0)
	for rows.Next() {
		var (
			c         models.Customer
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(
			&c.ID,
			&c.ShopID,
			&c.Name,
			&c.Phone,
			&c.Notes,
			&c.OutstandingBalance.Amount,
			&c.OutstandingBalance.Currency,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("storage: scan customer: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			c.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			c.UpdatedAt = t
		}
		customers = append(customers, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("storage: iterate customers: %w", err)
	}

	return customers, total, nil
}

// UpdateCustomer edits customer details (name, phone, notes). Outstanding balance is managed via debt transactions.
func (s *Store) UpdateCustomer(ctx context.Context, shopID string, c models.Customer) error {
	if strings.TrimSpace(shopID) == "" || strings.TrimSpace(c.ID) == "" {
		return errors.New("storage: shop_id and customer id required")
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		return errors.New("storage: customer name is required")
	}

	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	query := s.rebind(`
		UPDATE customers
		SET name = ?, phone = ?, notes = ?, updated_at = ?
		WHERE shop_id = ? AND id = ?
	`)

	res, err := s.db.ExecContext(ctx, query, name, c.Phone, c.Notes, updatedAt, shopID, c.ID)
	if err != nil {
		return fmt.Errorf("storage: update customer: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordCustomerDebt executes an atomic credit adjustment, updating customer outstanding balance and inserting debt ledger line.
func (s *Store) RecordCustomerDebt(ctx context.Context, debt models.CustomerDebt) (models.CustomerDebt, error) {
	if strings.TrimSpace(debt.ID) == "" {
		return models.CustomerDebt{}, errors.New("storage: debt id required")
	}
	if strings.TrimSpace(debt.ShopID) == "" {
		return models.CustomerDebt{}, errors.New("storage: shop_id required")
	}
	if strings.TrimSpace(debt.CustomerID) == "" {
		return models.CustomerDebt{}, errors.New("storage: customer_id required")
	}
	if !debt.Type.Valid() {
		return models.CustomerDebt{}, errors.New("storage: invalid debt type")
	}
	if debt.Amount.Amount <= 0 {
		return models.CustomerDebt{}, errors.New("storage: debt amount must be positive")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.CustomerDebt{}, fmt.Errorf("storage: begin debt tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		currentBalance int64
		currency       string
	)

	selectQuery := s.rebind(`SELECT outstanding_amount, outstanding_currency FROM customers WHERE shop_id = ? AND id = ?`)
	if s.dialect == DialectPostgres {
		selectQuery += " FOR UPDATE"
	}

	err = tx.QueryRowContext(ctx, selectQuery, debt.ShopID, debt.CustomerID).Scan(&currentBalance, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		return models.CustomerDebt{}, ErrNotFound
	}
	if err != nil {
		return models.CustomerDebt{}, fmt.Errorf("storage: lock customer for debt: %w", err)
	}

	var newBalance int64
	switch debt.Type {
	case models.DebtTypeCreditSale:
		newBalance = currentBalance + debt.Amount.Amount
	case models.DebtTypeRepayment:
		newBalance = currentBalance - debt.Amount.Amount
	case models.DebtTypeForgiveness:
		newBalance = currentBalance - debt.Amount.Amount
		if newBalance < 0 {
			newBalance = 0
		}
	}

	now := time.Now().UTC()
	if debt.OccurredAt.IsZero() {
		debt.OccurredAt = now
	}
	debt.CreatedAt = now
	debt.BalanceAfter = models.Money{Amount: newBalance, Currency: currency}

	if err := debt.Validate(); err != nil {
		return models.CustomerDebt{}, fmt.Errorf("storage: validate debt: %w", err)
	}

	occurredAtStr := debt.OccurredAt.UTC().Format(time.RFC3339Nano)
	createdAtStr := debt.CreatedAt.UTC().Format(time.RFC3339Nano)

	insertQuery := s.rebind(`
		INSERT INTO customer_debts (
			id,
			shop_id,
			customer_id,
			type,
			amount_amount,
			amount_currency,
			balance_after_amount,
			balance_after_currency,
			description,
			receipt_ref,
			occurred_at,
			created_by,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)

	_, err = tx.ExecContext(
		ctx,
		insertQuery,
		debt.ID,
		debt.ShopID,
		debt.CustomerID,
		string(debt.Type),
		debt.Amount.Amount,
		debt.Amount.Currency,
		debt.BalanceAfter.Amount,
		debt.BalanceAfter.Currency,
		debt.Description,
		debt.ReceiptRef,
		occurredAtStr,
		debt.CreatedBy,
		createdAtStr,
	)
	if err != nil {
		return models.CustomerDebt{}, fmt.Errorf("storage: insert customer debt: %w", err)
	}

	updateQuery := s.rebind(`
		UPDATE customers
		SET outstanding_amount = ?, updated_at = ?
		WHERE shop_id = ? AND id = ?
	`)
	_, err = tx.ExecContext(ctx, updateQuery, newBalance, createdAtStr, debt.ShopID, debt.CustomerID)
	if err != nil {
		return models.CustomerDebt{}, fmt.Errorf("storage: update customer balance: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return models.CustomerDebt{}, fmt.Errorf("storage: commit debt transaction: %w", err)
	}

	return debt, nil
}

// ListCustomerDebts fetches historical credit and repayment entries for a customer.
func (s *Store) ListCustomerDebts(ctx context.Context, shopID, customerID string, limit, offset int) ([]models.CustomerDebt, int, error) {
	if strings.TrimSpace(shopID) == "" || strings.TrimSpace(customerID) == "" {
		return nil, 0, errors.New("storage: shop_id and customer_id required")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	countQuery := s.rebind(`SELECT COUNT(*) FROM customer_debts WHERE shop_id = ? AND customer_id = ?`)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, shopID, customerID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("storage: count customer debts: %w", err)
	}

	query := s.rebind(`
		SELECT
			id,
			shop_id,
			customer_id,
			type,
			amount_amount,
			amount_currency,
			balance_after_amount,
			balance_after_currency,
			description,
			receipt_ref,
			occurred_at,
			created_by,
			created_at
		FROM customer_debts
		WHERE shop_id = ? AND customer_id = ?
		ORDER BY occurred_at DESC, id DESC
		LIMIT ? OFFSET ?
	`)

	rows, err := s.db.QueryContext(ctx, query, shopID, customerID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: list customer debts: %w", err)
	}
	defer rows.Close()

	debts := make([]models.CustomerDebt, 0)
	for rows.Next() {
		var (
			d             models.CustomerDebt
			typeStr       string
			occurredAtStr string
			createdAtStr  string
		)
		if err := rows.Scan(
			&d.ID,
			&d.ShopID,
			&d.CustomerID,
			&typeStr,
			&d.Amount.Amount,
			&d.Amount.Currency,
			&d.BalanceAfter.Amount,
			&d.BalanceAfter.Currency,
			&d.Description,
			&d.ReceiptRef,
			&occurredAtStr,
			&d.CreatedBy,
			&createdAtStr,
		); err != nil {
			return nil, 0, fmt.Errorf("storage: scan customer debt: %w", err)
		}
		d.Type = models.CustomerDebtType(typeStr)
		if t, err := time.Parse(time.RFC3339Nano, occurredAtStr); err == nil {
			d.OccurredAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
			d.CreatedAt = t
		}
		debts = append(debts, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("storage: iterate customer debts: %w", err)
	}

	return debts, total, nil
}

// GetShopCreditSummary computes total outstanding debt across customers and today's credit/repayment movements.
func (s *Store) GetShopCreditSummary(ctx context.Context, shopID string, todayStart, todayEnd time.Time) (ShopCreditSummary, error) {
	if strings.TrimSpace(shopID) == "" {
		return ShopCreditSummary{}, errors.New("storage: shop_id is required")
	}

	shop, err := s.GetShop(ctx, shopID)
	if err != nil {
		return ShopCreditSummary{}, fmt.Errorf("storage: get shop currency: %w", err)
	}

	summary := ShopCreditSummary{
		ShopID:   shopID,
		Currency: shop.Currency,
		TotalOutstandingDebt: models.Money{
			Currency: shop.Currency,
		},
		TodayCreditSalesAmount: models.Money{
			Currency: shop.Currency,
		},
		TodayRepaymentsAmount: models.Money{
			Currency: shop.Currency,
		},
	}

	// 1. Total customers with debt & total outstanding debt
	custQuery := s.rebind(`
		SELECT
			COUNT(*),
			COALESCE(SUM(outstanding_amount), 0)
		FROM customers
		WHERE shop_id = ? AND outstanding_amount > 0
	`)
	if err := s.db.QueryRowContext(ctx, custQuery, shopID).Scan(
		&summary.TotalCustomersWithDebt,
		&summary.TotalOutstandingDebt.Amount,
	); err != nil {
		return summary, fmt.Errorf("storage: query customer balances: %w", err)
	}

	// 2. Today's credit sales & repayments
	startStr := todayStart.UTC().Format(time.RFC3339Nano)
	endStr := todayEnd.UTC().Format(time.RFC3339Nano)

	movementsQuery := s.rebind(`
		SELECT
			type,
			COALESCE(SUM(amount_amount), 0)
		FROM customer_debts
		WHERE shop_id = ? AND occurred_at >= ? AND occurred_at <= ?
		GROUP BY type
	`)

	rows, err := s.db.QueryContext(ctx, movementsQuery, shopID, startStr, endStr)
	if err != nil {
		return summary, fmt.Errorf("storage: query debt movements: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			dType string
			sum   int64
		)
		if err := rows.Scan(&dType, &sum); err != nil {
			return summary, fmt.Errorf("storage: scan debt movement sum: %w", err)
		}
		switch models.CustomerDebtType(dType) {
		case models.DebtTypeCreditSale:
			summary.TodayCreditSalesAmount.Amount = sum
		case models.DebtTypeRepayment:
			summary.TodayRepaymentsAmount.Amount = sum
		}
	}
	if err := rows.Err(); err != nil {
		return summary, fmt.Errorf("storage: iterate debt movement sums: %w", err)
	}

	return summary, nil
}
