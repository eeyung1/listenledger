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

// ProductMovement records a single historical stock adjustment, linked to its triggering transaction.
type ProductMovement struct {
	TransactionID string                   `json:"transaction_id"`
	ShopID        string                   `json:"shop_id"`
	ProductID     string                   `json:"product_id"`
	ProductName   string                   `json:"product_name"`
	Type          models.TransactionType   `json:"type"`
	Status        models.TransactionStatus `json:"status"`
	Quantity      int                      `json:"quantity"`
	Unit          models.Unit              `json:"unit"`
	Delta         int                      `json:"delta"`
	CreatedAt     time.Time                `json:"created_at"`
	Transcript    string                   `json:"transcript,omitempty"`
}

// ReverseResult holds the updated original transaction, the newly created cancellation record,
// and the updated product with its restored inventory.
type ReverseResult struct {
	Original models.Transaction `json:"original"`
	Reversal models.Transaction `json:"reversal"`
	Product  *models.Product    `json:"product,omitempty"`
}

// ReverseTransaction voids a previously confirmed transaction, atomically reversing its stock effect
// and recording an explicit inverse stock movement row for auditing.
func (s *Store) ReverseTransaction(
	ctx context.Context,
	shopID, transactionID, reason string,
) (ReverseResult, error) {
	if shopID == "" || transactionID == "" {
		return ReverseResult{}, errors.New("storage: shop_id and transaction_id are required")
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Transaction voided by shop owner"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReverseResult{}, err
	}
	defer tx.Rollback()

	// 1. Fetch original transaction
	query := `SELECT ` + transactionColumns + ` FROM transactions WHERE shop_id = ? AND id = ?`
	orig, err := scanTransaction(tx.QueryRowContext(ctx, query, shopID, transactionID))
	if errors.Is(err, sql.ErrNoRows) {
		return ReverseResult{}, ErrNotFound
	}
	if err != nil {
		return ReverseResult{}, err
	}

	if orig.Status != models.StatusConfirmed {
		return ReverseResult{}, fmt.Errorf("%w: only confirmed transactions can be reversed (current: %s)", ErrConflict, orig.Status)
	}

	var product *models.Product
	var productDelta int

	// 2. Compute inverse stock delta if product is associated
	if orig.ProductID != "" {
		p, err := s.getProduct(ctx, tx, `SELECT `+productColumns+` FROM products WHERE shop_id = ? AND id = ?`, shopID, orig.ProductID)
		if err != nil {
			return ReverseResult{}, err
		}

		baseUnits, err := models.BaseQuantity(p, orig.Quantity, orig.Unit)
		if err != nil {
			return ReverseResult{}, err
		}

		// A confirmed sale reduced stock; reversing it adds stock back.
		// A confirmed restock added stock; reversing it removes stock.
		if orig.Type == models.TypeSale {
			productDelta = baseUnits
		} else {
			productDelta = -baseUnits
		}

		if productDelta < 0 && p.Quantity < -productDelta {
			return ReverseResult{}, ErrInsufficientStock
		}

		p.Quantity += productDelta

		_, err = tx.ExecContext(ctx,
			`UPDATE products SET quantity = ?, updated_at = ? WHERE shop_id = ? AND id = ?`,
			p.Quantity, formatTime(time.Now()), shopID, p.ID)
		if err != nil {
			return ReverseResult{}, fmt.Errorf("storage: update product stock for reversal: %w", err)
		}

		product = &p
	}

	// 3. Mark original transaction as cancelled
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx,
		`UPDATE transactions SET status = 'cancelled' WHERE shop_id = ? AND id = ?`,
		shopID, orig.ID)
	if err != nil {
		return ReverseResult{}, fmt.Errorf("storage: cancel original transaction: %w", err)
	}
	orig.Status = models.StatusCancelled

	// 4. Create explicit reversal transaction record
	reversalID := fmt.Sprintf("rev_%s_%d", orig.ID, now.UnixNano())
	if len(reversalID) > 64 {
		reversalID = reversalID[:64]
	}

	reversalTxn := models.Transaction{
		ID:          reversalID,
		ShopID:      orig.ShopID,
		Type:        orig.Type,
		ProductID:   orig.ProductID,
		ProductName: orig.ProductName,
		Quantity:    orig.Quantity,
		Unit:        orig.Unit,
		Amount:      orig.Amount,
		Confidence:  models.Confidence{Overall: 1.0},
		Status:      models.StatusCancelled,
		OccurredAt:  now,
		CreatedAt:   now,
		Transcript:  fmt.Sprintf("VOID [%s]: %s", orig.ID, reason),
	}

	if err := insertTransaction(ctx, tx, reversalTxn); err != nil {
		return ReverseResult{}, fmt.Errorf("storage: insert reversal transaction: %w", err)
	}

	// 5. Insert inverse stock movement row
	if orig.ProductID != "" && productDelta != 0 {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO stock_movements(transaction_id, shop_id, product_id, delta, created_at) VALUES(?, ?, ?, ?, ?)`,
			reversalID, shopID, orig.ProductID, productDelta, formatTime(now))
		if err != nil {
			return ReverseResult{}, fmt.Errorf("storage: insert reversal stock movement: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return ReverseResult{}, err
	}

	return ReverseResult{
		Original: orig,
		Reversal: reversalTxn,
		Product:  product,
	}, nil
}

// GetProductMovements queries the chronological stock movement ledger for a product.
func (s *Store) GetProductMovements(
	ctx context.Context,
	shopID, productID string,
	limit, offset int,
) ([]ProductMovement, int, error) {
	if shopID == "" || productID == "" {
		return nil, 0, errors.New("storage: shop_id and product_id are required")
	}

	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stock_movements WHERE shop_id = ? AND product_id = ?`,
		shopID, productID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: count product movements: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT sm.transaction_id, sm.shop_id, sm.product_id, sm.delta, sm.created_at,
		       t.type, t.status, t.quantity, t.unit, t.product_name, COALESCE(t.transcript, '')
		FROM stock_movements sm
		JOIN transactions t ON sm.shop_id = t.shop_id AND sm.transaction_id = t.id
		WHERE sm.shop_id = ? AND sm.product_id = ?
		ORDER BY sm.created_at DESC
		LIMIT ? OFFSET ?
	`, shopID, productID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: query product movements: %w", err)
	}
	defer rows.Close()

	var movements []ProductMovement
	for rows.Next() {
		var m ProductMovement
		var createdAtRaw string
		var tType, tStatus, tUnit string

		err := rows.Scan(
			&m.TransactionID,
			&m.ShopID,
			&m.ProductID,
			&m.Delta,
			&createdAtRaw,
			&tType,
			&tStatus,
			&m.Quantity,
			&tUnit,
			&m.ProductName,
			&m.Transcript,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("storage: scan product movement: %w", err)
		}

		m.Type = models.TransactionType(tType)
		m.Status = models.TransactionStatus(tStatus)
		m.Unit = models.Unit(tUnit)
		parsedTime, _ := time.Parse(time.RFC3339Nano, createdAtRaw)
		m.CreatedAt = parsedTime

		movements = append(movements, m)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	if movements == nil {
		movements = []ProductMovement{}
	}

	return movements, total, nil
}

// AdjustProductStock performs a reasoned physical stock count correction with an audit transaction.
func (s *Store) AdjustProductStock(
	ctx context.Context,
	shopID, productID string,
	newQty int,
	reason string,
) (models.Product, models.Transaction, error) {
	if shopID == "" || productID == "" {
		return models.Product{}, models.Transaction{}, errors.New("storage: shop_id and product_id are required")
	}
	if newQty < 0 {
		return models.Product{}, models.Transaction{}, errors.New("storage: stock quantity cannot be negative")
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Physical stock count adjustment"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Product{}, models.Transaction{}, err
	}
	defer tx.Rollback()

	p, err := s.getProduct(ctx, tx, `SELECT `+productColumns+` FROM products WHERE shop_id = ? AND id = ?`, shopID, productID)
	if err != nil {
		return models.Product{}, models.Transaction{}, err
	}

	delta := newQty - p.Quantity
	if delta == 0 {
		return p, models.Transaction{}, nil
	}

	p.Quantity = newQty

	_, err = tx.ExecContext(ctx,
		`UPDATE products SET quantity = ?, updated_at = ? WHERE shop_id = ? AND id = ?`,
		p.Quantity, formatTime(time.Now()), shopID, p.ID)
	if err != nil {
		return models.Product{}, models.Transaction{}, fmt.Errorf("storage: update product stock: %w", err)
	}

	now := time.Now().UTC()
	adjID := fmt.Sprintf("adj_%s_%d", p.ID, now.UnixNano())
	if len(adjID) > 64 {
		adjID = adjID[:64]
	}

	txnType := models.TypeRestock
	absQty := delta
	if delta < 0 {
		txnType = models.TypeSale
		absQty = -delta
	}

	adjTxn := models.Transaction{
		ID:          adjID,
		ShopID:      shopID,
		Type:        txnType,
		ProductID:   p.ID,
		ProductName: p.Name,
		Quantity:    absQty,
		Unit:        p.BaseUnit,
		Amount:      models.Money{Amount: 0, Currency: p.Price.Currency},
		Confidence:  models.Confidence{Overall: 1.0},
		Status:      models.StatusConfirmed,
		OccurredAt:  now,
		CreatedAt:   now,
		Transcript:  fmt.Sprintf("STOCK AUDIT (%+d %s): %s", delta, p.BaseUnit, reason),
	}

	if err := insertTransaction(ctx, tx, adjTxn); err != nil {
		return models.Product{}, models.Transaction{}, fmt.Errorf("storage: insert audit transaction: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO stock_movements(transaction_id, shop_id, product_id, delta, created_at) VALUES(?, ?, ?, ?, ?)`,
		adjID, shopID, p.ID, delta, formatTime(now))
	if err != nil {
		return models.Product{}, models.Transaction{}, fmt.Errorf("storage: insert stock movement: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return models.Product{}, models.Transaction{}, err
	}

	return p, adjTxn, nil
}
