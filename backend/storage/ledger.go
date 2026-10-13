package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

var ErrConflict = errors.New("idempotency key was already used for different content")
var ErrCurrency = errors.New("currency must match the shop and product currency")
var ErrInvalidMovement = errors.New("invalid stock movement")

type LedgerResult struct {
	Transaction models.Transaction
	Product     *models.Product
	Replayed    bool
}

// RecordMovement holds SQLite's immediate write lock through validation, stock and ledger writes.
func (s *Store) RecordMovement(ctx context.Context, t models.Transaction, key, fingerprint string) (LedgerResult, error) {
	if key == "" || len(key) > 128 || fingerprint == "" || t.ShopID == "" || t.ID == "" || t.Quantity <= 0 || t.Amount.Amount < 0 || !t.Type.Valid() || (t.Status != models.StatusConfirmed && t.Status != models.StatusReviewRequired) {
		return LedgerResult{}, ErrInvalidMovement
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LedgerResult{}, err
	}
	defer tx.Rollback()
	var previousHash, id string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,transaction_id FROM ledger_requests WHERE shop_id=? AND request_key=?`, t.ShopID, key).Scan(&previousHash, &id)
	if err == nil {
		if previousHash != fingerprint {
			return LedgerResult{}, ErrConflict
		}
		previous, err := scanTransaction(tx.QueryRowContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE shop_id=? AND id=?`, t.ShopID, id))
		if err != nil {
			return LedgerResult{}, err
		}
		return LedgerResult{Transaction: previous, Replayed: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LedgerResult{}, err
	}
	var currency string
	if err = tx.QueryRowContext(ctx, `SELECT currency FROM shops WHERE id=?`, t.ShopID).Scan(&currency); err != nil {
		return LedgerResult{}, err
	}
	if currency != t.Amount.Currency {
		return LedgerResult{}, ErrCurrency
	}
	var product *models.Product
	if t.ProductID != "" {
		p, err := s.getProduct(ctx, tx, `SELECT `+productColumns+` FROM products WHERE shop_id=? AND id=?`, t.ShopID, t.ProductID)
		if err != nil {
			return LedgerResult{}, err
		}
		if p.Price.Currency != currency {
			return LedgerResult{}, ErrCurrency
		}
		product = &p
		t.ProductName = p.Name
	} else {
		t.Status = models.StatusReviewRequired
	}
	var delta int
	if t.Status == models.StatusConfirmed {
		delta, err = models.BaseQuantity(*product, t.Quantity, t.Unit)
		if err != nil {
			return LedgerResult{}, err
		}
		if t.Type == models.TypeSale {
			delta = -delta
		}
		if delta < 0 && product.Quantity < -delta {
			return LedgerResult{}, ErrInsufficientStock
		}
		if delta > 0 && product.Quantity > math.MaxInt-delta {
			return LedgerResult{}, ErrInvalidMovement
		}
		product.Quantity += delta
		if _, err = tx.ExecContext(ctx, `UPDATE products SET quantity=?,updated_at=? WHERE shop_id=? AND id=?`, product.Quantity, formatTime(time.Now()), t.ShopID, t.ProductID); err != nil {
			return LedgerResult{}, err
		}
	}
	if t.OccurredAt.IsZero() {
		t.OccurredAt = time.Now().UTC()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if err = insertTransaction(ctx, tx, t); err != nil {
		return LedgerResult{}, err
	}
	if delta != 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO stock_movements(transaction_id,shop_id,product_id,delta,created_at) VALUES(?,?,?,?,?)`, t.ID, t.ShopID, t.ProductID, delta, formatTime(t.CreatedAt)); err != nil {
			return LedgerResult{}, err
		}
	} else {
		product = nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ledger_requests(shop_id,request_key,payload_hash,transaction_id) VALUES(?,?,?,?)`, t.ShopID, key, fingerprint, t.ID); err != nil {
		return LedgerResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return LedgerResult{}, err
	}
	return LedgerResult{Transaction: t, Product: product}, nil
}
