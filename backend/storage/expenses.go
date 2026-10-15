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

// ExpenseQuery specifies filters and pagination for listing expenses.
type ExpenseQuery struct {
	ShopID   string
	Category models.ExpenseCategory
	From     *time.Time
	To       *time.Time
	Limit    int
	Offset   int
}

// ExpenseSummary aggregates expense statistics for a time window.
type ExpenseSummary struct {
	ShopID      string                           `json:"shop_id"`
	Currency    string                           `json:"currency"`
	TotalAmount models.Money                     `json:"total_amount"`
	TotalCount  int                              `json:"total_count"`
	ByCategory  map[models.ExpenseCategory]int64 `json:"by_category"`
}

// CreateExpense inserts a new operating expense into the store.
func (s *Store) CreateExpense(ctx context.Context, exp models.Expense) error {
	if err := exp.Validate(); err != nil {
		return fmt.Errorf("storage: validate expense: %w", err)
	}

	query := s.rebind(`
		INSERT INTO expenses (
			id,
			shop_id,
			category,
			amount_amount,
			amount_currency,
			description,
			receipt_ref,
			occurred_at,
			created_by,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)

	_, err := s.db.ExecContext(
		ctx,
		query,
		exp.ID,
		exp.ShopID,
		string(exp.Category),
		exp.Amount.Amount,
		exp.Amount.Currency,
		exp.Description,
		exp.ReceiptRef,
		formatTime(exp.OccurredAt),
		exp.CreatedBy,
		formatTime(exp.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("storage: insert expense: %w", err)
	}
	return nil
}

// GetExpenseByID fetches a single expense record scoped to a shop.
func (s *Store) GetExpenseByID(ctx context.Context, shopID, id string) (models.Expense, error) {
	if shopID == "" || id == "" {
		return models.Expense{}, errors.New("storage: shop_id and id are required")
	}

	query := s.rebind(`
		SELECT
			id,
			shop_id,
			category,
			amount_amount,
			amount_currency,
			description,
			receipt_ref,
			occurred_at,
			created_by,
			created_at
		FROM expenses
		WHERE shop_id = ? AND id = ?
	`)

	var (
		exp           models.Expense
		cat           string
		occurredAtRaw string
		createdAtRaw  string
	)

	err := s.db.QueryRowContext(ctx, query, shopID, id).Scan(
		&exp.ID,
		&exp.ShopID,
		&cat,
		&exp.Amount.Amount,
		&exp.Amount.Currency,
		&exp.Description,
		&exp.ReceiptRef,
		&occurredAtRaw,
		&exp.CreatedBy,
		&createdAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Expense{}, ErrNotFound
	}
	if err != nil {
		return models.Expense{}, fmt.Errorf("storage: get expense: %w", err)
	}

	exp.Category = models.ExpenseCategory(cat)
	if exp.OccurredAt, err = parseTime(occurredAtRaw); err != nil {
		return models.Expense{}, fmt.Errorf("storage: parse occurred_at: %w", err)
	}
	if exp.CreatedAt, err = parseTime(createdAtRaw); err != nil {
		return models.Expense{}, fmt.Errorf("storage: parse created_at: %w", err)
	}

	return exp, nil
}

// ListExpenses retrieves a paginated and filtered list of expenses for a shop.
func (s *Store) ListExpenses(ctx context.Context, q ExpenseQuery) ([]models.Expense, int, error) {
	if q.ShopID == "" {
		return nil, 0, errors.New("storage: shop_id is required")
	}

	var where []string
	var args []any

	where = append(where, "shop_id = ?")
	args = append(args, q.ShopID)

	if q.Category != "" && q.Category.Valid() {
		where = append(where, "category = ?")
		args = append(args, string(q.Category))
	}
	if q.From != nil {
		where = append(where, "occurred_at >= ?")
		args = append(args, formatTime(*q.From))
	}
	if q.To != nil {
		where = append(where, "occurred_at <= ?")
		args = append(args, formatTime(*q.To))
	}

	whereClause := strings.Join(where, " AND ")

	// 1. Get total count
	countQuery := s.rebind(fmt.Sprintf("SELECT COUNT(*) FROM expenses WHERE %s", whereClause))
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("storage: count expenses: %w", err)
	}

	// 2. Clamp pagination
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	// 3. Fetch records
	dataQuery := s.rebind(fmt.Sprintf(`
		SELECT
			id,
			shop_id,
			category,
			amount_amount,
			amount_currency,
			description,
			receipt_ref,
			occurred_at,
			created_by,
			created_at
		FROM expenses
		WHERE %s
		ORDER BY occurred_at DESC, id DESC
		LIMIT ? OFFSET ?
	`, whereClause))

	dataArgs := append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: list expenses: %w", err)
	}
	defer rows.Close()

	expenses := make([]models.Expense, 0)
	for rows.Next() {
		var (
			exp           models.Expense
			cat           string
			occurredAtRaw string
			createdAtRaw  string
		)
		err := rows.Scan(
			&exp.ID,
			&exp.ShopID,
			&cat,
			&exp.Amount.Amount,
			&exp.Amount.Currency,
			&exp.Description,
			&exp.ReceiptRef,
			&occurredAtRaw,
			&exp.CreatedBy,
			&createdAtRaw,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("storage: scan expense: %w", err)
		}
		exp.Category = models.ExpenseCategory(cat)
		if exp.OccurredAt, err = parseTime(occurredAtRaw); err != nil {
			return nil, 0, fmt.Errorf("storage: parse expense occurred_at: %w", err)
		}
		if exp.CreatedAt, err = parseTime(createdAtRaw); err != nil {
			return nil, 0, fmt.Errorf("storage: parse expense created_at: %w", err)
		}
		expenses = append(expenses, exp)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("storage: iterate expenses: %w", err)
	}

	return expenses, total, nil
}

// DeleteExpense removes an expense record scoped to a shop.
func (s *Store) DeleteExpense(ctx context.Context, shopID, id string) error {
	if shopID == "" || id == "" {
		return errors.New("storage: shop_id and id are required")
	}

	query := s.rebind(`DELETE FROM expenses WHERE shop_id = ? AND id = ?`)
	res, err := s.db.ExecContext(ctx, query, shopID, id)
	if err != nil {
		return fmt.Errorf("storage: delete expense: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: rows affected delete expense: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// GetExpenseSummary aggregates expense totals and category breakdowns for a given date range.
func (s *Store) GetExpenseSummary(ctx context.Context, shopID string, startUTC, endUTC time.Time) (ExpenseSummary, error) {
	if shopID == "" {
		return ExpenseSummary{}, errors.New("storage: shop_id is required")
	}

	var currency string
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT currency FROM shops WHERE id = ?`), shopID).Scan(&currency)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ExpenseSummary{}, fmt.Errorf("storage: get shop currency: %w", err)
	}
	if currency == "" {
		currency = "NGN"
	}

	summary := ExpenseSummary{
		ShopID:     shopID,
		Currency:   currency,
		ByCategory: make(map[models.ExpenseCategory]int64),
	}

	query := s.rebind(`
		SELECT category, COALESCE(SUM(amount_amount), 0), COUNT(id)
		FROM expenses
		WHERE shop_id = ?
		  AND occurred_at >= ?
		  AND occurred_at < ?
		GROUP BY category
	`)

	rows, err := s.db.QueryContext(ctx, query, shopID, formatTime(startUTC), formatTime(endUTC))
	if err != nil {
		return ExpenseSummary{}, fmt.Errorf("storage: query expense breakdown: %w", err)
	}
	defer rows.Close()

	var totalSum int64
	var totalCount int

	for rows.Next() {
		var (
			cat   string
			sum   int64
			count int
		)
		if err := rows.Scan(&cat, &sum, &count); err != nil {
			return ExpenseSummary{}, fmt.Errorf("storage: scan expense breakdown: %w", err)
		}
		c := models.ExpenseCategory(cat)
		summary.ByCategory[c] = sum
		totalSum += sum
		totalCount += count
	}

	if err := rows.Err(); err != nil {
		return ExpenseSummary{}, fmt.Errorf("storage: iterate expense breakdown: %w", err)
	}

	summary.TotalAmount = models.Money{
		Amount:   totalSum,
		Currency: currency,
	}
	summary.TotalCount = totalCount

	return summary, nil
}
