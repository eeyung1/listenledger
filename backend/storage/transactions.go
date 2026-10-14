package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

// Snapshot paging bounds. Exported so the HTTP layer clamps to the same
// numbers the store does, rather than inventing its own.
const (
	DefaultTransactionLimit = 50
	MaxTransactionLimit     = 500
)

// ClampLimit normalises a requested page size into the supported range.
func ClampLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultTransactionLimit
	case limit > MaxTransactionLimit:
		return MaxTransactionLimit
	default:
		return limit
	}
}

// transactionColumns is the column list every transaction read uses, in the
// order scanTransaction expects.
const transactionColumns = `id, shop_id, type, product_id, product_name, quantity,
	unit, amount_amount, amount_currency, confidence_overall, confidence_product,
	confidence_quantity, confidence_amount, status, occurred_at, created_at,
	transcript, receipt_id`

// InsertTransaction records a stock movement.
//
// The caller supplies the ID and must set Status. Both a Review Required row
// and a Confirmed one are inserted here: the capture is stored whatever the
// validation engine concluded, and the review flow later changes only the
// status. Nothing is lost by holding a low-confidence capture.
func (s *Store) InsertTransaction(ctx context.Context, transaction models.Transaction) error {
	return s.insertTransaction(ctx, s.db, transaction)
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) insertTransaction(ctx context.Context, db executor, transaction models.Transaction) error {
	if transaction.ShopID == "" {
		return errors.New("storage: InsertTransaction requires a shop_id")
	}
	if transaction.ID == "" {
		return errors.New("storage: InsertTransaction requires an id")
	}
	// SQLite has no enum type, so the vocabulary is enforced here. An
	// unrecognised status would silently fall outside every query that
	// filters on it.
	if !transaction.Status.Valid() {
		return fmt.Errorf("storage: insert transaction %s: invalid status %q",
			transaction.ID, transaction.Status)
	}
	if !transaction.Type.Valid() {
		return fmt.Errorf("storage: insert transaction %s: invalid type %q",
			transaction.ID, transaction.Type)
	}

	occurredAt := transaction.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	createdAt := transaction.CreatedAt
	if createdAt.IsZero() {
		createdAt = occurredAt
	}

	const query = `
		INSERT INTO transactions (
			id, shop_id, type, product_id, product_name, quantity, unit,
			amount_amount, amount_currency, confidence_overall,
			confidence_product, confidence_quantity, confidence_amount,
			status, occurred_at, created_at, transcript, receipt_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := db.ExecContext(ctx, s.rebind(query),
		transaction.ID, transaction.ShopID, string(transaction.Type),
		nullString(transaction.ProductID), transaction.ProductName,
		transaction.Quantity, string(transaction.Unit),
		transaction.Amount.Amount, transaction.Amount.Currency,
		transaction.Confidence.Overall,
		nullFloat(transaction.Confidence.Product),
		nullFloat(transaction.Confidence.Quantity),
		nullFloat(transaction.Confidence.Amount),
		string(transaction.Status), formatTime(occurredAt), formatTime(createdAt),
		transaction.Transcript, nullString(transaction.ReceiptID),
	)
	if err != nil {
		return fmt.Errorf("storage: insert transaction %s: %w", transaction.ID, err)
	}
	return nil
}

// TransactionFilter defines query constraints for filtering and paging transactions.
type TransactionFilter struct {
	Type      *models.TransactionType
	Status    *models.TransactionStatus
	ProductID string
	ReceiptID string
	From      *time.Time
	To        *time.Time
	Limit     int
	Offset    int
}

// QueryTransactions returns movements for a shop matching the given filter, along with the total count.
func (s *Store) QueryTransactions(ctx context.Context, shopID string, filter TransactionFilter) ([]models.Transaction, int, error) {
	if shopID == "" {
		return nil, 0, errors.New("storage: QueryTransactions requires a shop_id")
	}

	whereClause := "WHERE shop_id = ?"
	args := []any{shopID}

	if filter.Type != nil && filter.Type.Valid() {
		whereClause += " AND type = ?"
		args = append(args, string(*filter.Type))
	}
	if filter.Status != nil && filter.Status.Valid() {
		whereClause += " AND status = ?"
		args = append(args, string(*filter.Status))
	}
	if filter.ProductID != "" {
		whereClause += " AND product_id = ?"
		args = append(args, filter.ProductID)
	}
	if filter.ReceiptID != "" {
		whereClause += " AND receipt_id = ?"
		args = append(args, filter.ReceiptID)
	}
	if filter.From != nil {
		whereClause += " AND occurred_at >= ?"
		args = append(args, formatTime(*filter.From))
	}
	if filter.To != nil {
		whereClause += " AND occurred_at <= ?"
		args = append(args, formatTime(*filter.To))
	}

	countQuery := s.rebind("SELECT COUNT(*) FROM transactions " + whereClause)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("storage: count transactions: %w", err)
	}

	limit := ClampLimit(filter.Limit)
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	dataQuery := s.rebind("SELECT " + transactionColumns + " FROM transactions " + whereClause + " ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?")
	dataArgs := append(append([]any{}, args...), limit, offset)

	rows, err := s.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: query transactions: %w", err)
	}
	defer rows.Close()

	// Non-nil so an empty ledger encodes as [] rather than null.
	transactions := make([]models.Transaction, 0)
	for rows.Next() {
		transaction, err := scanTransaction(rows)
		if err != nil {
			return nil, 0, err
		}
		transactions = append(transactions, transaction)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("storage: iterate transactions: %w", err)
	}
	return transactions, total, nil
}

// GetTransactions returns the newest movements for a shop.
//
// This is the snapshot a dashboard loads on first paint and refetches after a
// reconnect has left a gap in the event stream. Ordering is by occurred_at
// with id as a tie-break, so the sequence is total and a client that re-reads
// the same page sees the same order.
func (s *Store) GetTransactions(ctx context.Context, shopID string, limit int) ([]models.Transaction, error) {
	txns, _, err := s.QueryTransactions(ctx, shopID, TransactionFilter{Limit: limit})
	return txns, err
}

// GetTransactionByID returns one movement, scoped to its shop.
func (s *Store) GetTransactionByID(ctx context.Context, shopID, transactionID string) (models.Transaction, error) {
	const query = `SELECT ` + transactionColumns + `
		  FROM transactions
		 WHERE shop_id = ? AND id = ?`

	transaction, err := scanTransaction(s.db.QueryRowContext(ctx, s.rebind(query), shopID, transactionID))
	if errors.Is(err, sql.ErrNoRows) {
		return models.Transaction{}, ErrNotFound
	}
	if err != nil {
		return models.Transaction{}, err
	}
	return transaction, nil
}

// UpdateTransactionStatus moves a record through its lifecycle, which is how
// the Review Queue confirms or rejects a held capture.
//
// It changes the status only — it does not touch stock. Resolving a review
// means updating the row *and* applying its inventory effect, and those must
// commit together or a crash between them either loses a sale or double-counts
// one. That combined operation belongs in a service method wrapping both this
// and AdjustStock in a single transaction; it is not implemented yet, and
// calling this on its own will leave stock untouched.
func (s *Store) UpdateTransactionStatus(
	ctx context.Context,
	shopID, transactionID string,
	status models.TransactionStatus,
) (models.Transaction, error) {
	if !status.Valid() {
		return models.Transaction{}, fmt.Errorf("storage: invalid status %q", status)
	}

	const query = `UPDATE transactions SET status = ? WHERE shop_id = ? AND id = ?`
	result, err := s.db.ExecContext(ctx, s.rebind(query), string(status), shopID, transactionID)
	if err != nil {
		return models.Transaction{}, fmt.Errorf("storage: update transaction %s: %w", transactionID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return models.Transaction{}, fmt.Errorf("storage: update transaction %s: %w", transactionID, err)
	}
	if affected == 0 {
		return models.Transaction{}, ErrNotFound
	}
	return s.GetTransactionByID(ctx, shopID, transactionID)
}

// CountTransactionsByStatus counts a shop's records in one state, which is what
// the dashboard's "needs review" figure is built from.
func (s *Store) CountTransactionsByStatus(
	ctx context.Context,
	shopID string,
	status models.TransactionStatus,
) (int, error) {
	if shopID == "" {
		return 0, errors.New("storage: CountTransactionsByStatus requires a shop_id")
	}

	const query = `SELECT COUNT(*) FROM transactions WHERE shop_id = ? AND status = ?`
	var count int
	if err := s.db.QueryRowContext(ctx, s.rebind(query), shopID, string(status)).Scan(&count); err != nil {
		return 0, fmt.Errorf("storage: count transactions by status: %w", err)
	}
	return count, nil
}

func scanTransaction(row scanner) (models.Transaction, error) {
	var (
		transaction        models.Transaction
		kind               string
		productID          sql.NullString
		unit               string
		amountAmount       int64
		amountCurrency     string
		confidenceProduct  sql.NullFloat64
		confidenceQuantity sql.NullFloat64
		confidenceAmount   sql.NullFloat64
		status             string
		occurredAtRaw      string
		createdAtRaw       string
		receiptID          sql.NullString
	)

	err := row.Scan(
		&transaction.ID, &transaction.ShopID, &kind, &productID,
		&transaction.ProductName, &transaction.Quantity, &unit,
		&amountAmount, &amountCurrency, &transaction.Confidence.Overall,
		&confidenceProduct, &confidenceQuantity, &confidenceAmount, &status,
		&occurredAtRaw, &createdAtRaw, &transaction.Transcript, &receiptID,
	)
	if err != nil {
		return models.Transaction{}, err
	}

	transaction.Type = models.TransactionType(kind)
	transaction.Unit = models.Unit(unit)
	transaction.Status = models.TransactionStatus(status)
	transaction.ProductID = productID.String
	transaction.Amount = models.Money{Amount: amountAmount, Currency: amountCurrency}
	transaction.Confidence.Product = floatPointer(confidenceProduct)
	transaction.Confidence.Quantity = floatPointer(confidenceQuantity)
	transaction.Confidence.Amount = floatPointer(confidenceAmount)
	transaction.ReceiptID = receiptID.String

	if transaction.OccurredAt, err = parseTime(occurredAtRaw); err != nil {
		return models.Transaction{}, err
	}
	if transaction.CreatedAt, err = parseTime(createdAtRaw); err != nil {
		return models.Transaction{}, err
	}
	return transaction, nil
}

// floatPointer turns a nullable REAL back into a pointer, preserving the
// difference between "not scored" and "scored zero".
func floatPointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	score := value.Float64
	return &score
}

// PurgeTranscripts clears the spoken transcript text from historical transactions
// older than the specified timestamp for a given shop, honoring data retention and privacy policies
// while preserving financial audit records, ledger amounts, and inventory movements.
func (s *Store) PurgeTranscripts(ctx context.Context, shopID string, olderThan time.Time) (int64, error) {
	if shopID == "" {
		return 0, errors.New("storage: PurgeTranscripts requires a shop_id")
	}
	if olderThan.IsZero() {
		return 0, errors.New("storage: PurgeTranscripts requires a valid olderThan timestamp")
	}

	const query = `UPDATE transactions
                      SET transcript = ''
                    WHERE shop_id = ?
                      AND occurred_at < ?
                      AND transcript != ''`

	res, err := s.db.ExecContext(ctx, s.rebind(query), shopID, formatTime(olderThan))
	if err != nil {
		return 0, fmt.Errorf("storage: purge transcripts: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("storage: purge transcripts: rows affected: %w", err)
	}
	return affected, nil
}
