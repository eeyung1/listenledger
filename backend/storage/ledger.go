package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

var ErrConflict = errors.New("idempotency key was already used for different content")
var ErrCurrency = errors.New("currency must match the shop and product currency")
var ErrInvalidMovement = errors.New("invalid stock movement")

type ReviewDecisionType string

const (
	DecisionConfirmed             ReviewDecisionType = "confirmed"
	DecisionRejected              ReviewDecisionType = "rejected"
	DecisionCorrectedAndConfirmed ReviewDecisionType = "corrected_and_confirmed"
)

type ReviewValuesSnapshot struct {
	ProductID   string       `json:"product_id,omitempty"`
	ProductName string       `json:"product_name"`
	Quantity    int          `json:"quantity"`
	Unit        models.Unit  `json:"unit"`
	Amount      models.Money `json:"amount"`
}

type ReviewDecision struct {
	ID              string               `json:"id"`
	ShopID          string               `json:"shop_id"`
	TransactionID   string               `json:"transaction_id"`
	Decision        ReviewDecisionType   `json:"decision"`
	DecidedBy       string               `json:"decided_by"`
	DecidedAt       time.Time            `json:"decided_at"`
	OriginalValues  ReviewValuesSnapshot `json:"original_values"`
	CorrectedValues ReviewValuesSnapshot `json:"corrected_values,omitempty"`
	Notes           string               `json:"notes,omitempty"`
}

type LedgerResult struct {
	Transaction models.Transaction
	Product     *models.Product
	Decision    *ReviewDecision
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

type ReviewAction string

const (
	ReviewActionConfirm ReviewAction = "confirm"
	ReviewActionReject  ReviewAction = "reject"
)

type ReviewCorrections struct {
	ProductID string        `json:"product_id,omitempty"`
	Quantity  int           `json:"quantity,omitempty"`
	Unit      models.Unit   `json:"unit,omitempty"`
	Amount    *models.Money `json:"amount,omitempty"`
}

// ResolveReview resolves a held capture (review_required) by either confirming it
// with atomic stock adjustment or rejecting it, protected by immediate transaction locking
// and recording an immutable decision audit log.
func (s *Store) ResolveReview(
	ctx context.Context,
	shopID, transactionID string,
	action ReviewAction,
	corrections *ReviewCorrections,
	decidedBy string,
	notes string,
) (LedgerResult, error) {
	if shopID == "" || transactionID == "" {
		return LedgerResult{}, ErrInvalidMovement
	}
	if action != ReviewActionConfirm && action != ReviewActionReject {
		return LedgerResult{}, ErrInvalidMovement
	}
	if decidedBy == "" {
		decidedBy = "owner"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LedgerResult{}, err
	}
	defer tx.Rollback()

	t, err := scanTransaction(tx.QueryRowContext(ctx, `SELECT `+transactionColumns+` FROM transactions WHERE shop_id=? AND id=?`, shopID, transactionID))
	if errors.Is(err, sql.ErrNoRows) {
		return LedgerResult{}, ErrNotFound
	}
	if err != nil {
		return LedgerResult{}, err
	}

	if t.Status != models.StatusReviewRequired {
		return LedgerResult{}, ErrConflict
	}

	originalSnapshot := ReviewValuesSnapshot{
		ProductID:   t.ProductID,
		ProductName: t.ProductName,
		Quantity:    t.Quantity,
		Unit:        t.Unit,
		Amount:      t.Amount,
	}

	if action == ReviewActionReject {
		t.Status = models.StatusRejected
		if _, err := tx.ExecContext(ctx, `UPDATE transactions SET status=? WHERE shop_id=? AND id=?`, string(models.StatusRejected), shopID, transactionID); err != nil {
			return LedgerResult{}, err
		}

		now := time.Now().UTC()
		decisionID := fmt.Sprintf("dec_%d", now.UnixNano())
		decision := ReviewDecision{
			ID:             decisionID,
			ShopID:         shopID,
			TransactionID:  transactionID,
			Decision:       DecisionRejected,
			DecidedBy:      decidedBy,
			DecidedAt:      now,
			OriginalValues: originalSnapshot,
			Notes:          notes,
		}
		origJSON, _ := json.Marshal(decision.OriginalValues)
		if _, err := tx.ExecContext(ctx, `INSERT INTO review_decisions(id, shop_id, transaction_id, decision, decided_by, decided_at, original_values, corrected_values, notes) VALUES(?,?,?,?,?,?,?,?,?)`,
			decision.ID, shopID, transactionID, string(decision.Decision), decision.DecidedBy, formatTime(now), string(origJSON), "{}", decision.Notes); err != nil {
			return LedgerResult{}, err
		}

		if err := tx.Commit(); err != nil {
			return LedgerResult{}, err
		}
		return LedgerResult{Transaction: t, Product: nil, Decision: &decision}, nil
	}

	wasCorrected := false
	if corrections != nil {
		if corrections.ProductID != "" && corrections.ProductID != t.ProductID {
			t.ProductID = corrections.ProductID
			wasCorrected = true
		}
		if corrections.Quantity > 0 && corrections.Quantity != t.Quantity {
			t.Quantity = corrections.Quantity
			wasCorrected = true
		}
		if corrections.Unit.Valid() && corrections.Unit != t.Unit {
			t.Unit = corrections.Unit
			wasCorrected = true
		}
		if corrections.Amount != nil {
			if corrections.Amount.Amount < 0 {
				return LedgerResult{}, ErrInvalidMovement
			}
			if *corrections.Amount != t.Amount {
				t.Amount = *corrections.Amount
				wasCorrected = true
			}
		}
	}

	if t.ProductID == "" {
		return LedgerResult{}, errors.New("product is required to confirm transaction")
	}

	var currency string
	if err := tx.QueryRowContext(ctx, `SELECT currency FROM shops WHERE id=?`, shopID).Scan(&currency); err != nil {
		return LedgerResult{}, err
	}
	if currency != t.Amount.Currency {
		return LedgerResult{}, ErrCurrency
	}

	p, err := s.getProduct(ctx, tx, `SELECT `+productColumns+` FROM products WHERE shop_id=? AND id=?`, shopID, t.ProductID)
	if err != nil {
		return LedgerResult{}, err
	}
	if p.Price.Currency != currency {
		return LedgerResult{}, ErrCurrency
	}
	t.ProductName = p.Name

	delta, err := models.BaseQuantity(p, t.Quantity, t.Unit)
	if err != nil {
		return LedgerResult{}, err
	}
	if t.Type == models.TypeSale {
		delta = -delta
	}
	if delta < 0 && p.Quantity < -delta {
		return LedgerResult{}, ErrInsufficientStock
	}
	if delta > 0 && p.Quantity > math.MaxInt-delta {
		return LedgerResult{}, ErrInvalidMovement
	}
	p.Quantity += delta

	now := formatTime(time.Now().UTC())
	if _, err := tx.ExecContext(ctx, `UPDATE products SET quantity=?, updated_at=? WHERE shop_id=? AND id=?`, p.Quantity, now, shopID, t.ProductID); err != nil {
		return LedgerResult{}, err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO stock_movements(transaction_id, shop_id, product_id, delta, created_at) VALUES(?,?,?,?,?)`, t.ID, shopID, t.ProductID, delta, now); err != nil {
		return LedgerResult{}, err
	}

	t.Status = models.StatusConfirmed
	if _, err := tx.ExecContext(ctx, `UPDATE transactions SET status=?, product_id=?, product_name=?, quantity=?, unit=?, amount_amount=?, amount_currency=? WHERE shop_id=? AND id=?`,
		string(models.StatusConfirmed), t.ProductID, t.ProductName, t.Quantity, string(t.Unit), t.Amount.Amount, t.Amount.Currency, shopID, t.ID); err != nil {
		return LedgerResult{}, err
	}

	decisionType := DecisionConfirmed
	if wasCorrected {
		decisionType = DecisionCorrectedAndConfirmed
	}
	decidedNow := time.Now().UTC()
	decision := ReviewDecision{
		ID:             fmt.Sprintf("dec_%d", decidedNow.UnixNano()),
		ShopID:         shopID,
		TransactionID:  transactionID,
		Decision:       decisionType,
		DecidedBy:      decidedBy,
		DecidedAt:      decidedNow,
		OriginalValues: originalSnapshot,
		CorrectedValues: ReviewValuesSnapshot{
			ProductID:   t.ProductID,
			ProductName: t.ProductName,
			Quantity:    t.Quantity,
			Unit:        t.Unit,
			Amount:      t.Amount,
		},
		Notes: notes,
	}
	origJSON, _ := json.Marshal(decision.OriginalValues)
	corrJSON, _ := json.Marshal(decision.CorrectedValues)
	if _, err := tx.ExecContext(ctx, `INSERT INTO review_decisions(id, shop_id, transaction_id, decision, decided_by, decided_at, original_values, corrected_values, notes) VALUES(?,?,?,?,?,?,?,?,?)`,
		decision.ID, shopID, transactionID, string(decision.Decision), decision.DecidedBy, formatTime(decidedNow), string(origJSON), string(corrJSON), decision.Notes); err != nil {
		return LedgerResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return LedgerResult{}, err
	}

	return LedgerResult{Transaction: t, Product: &p, Decision: &decision}, nil
}

// GetReviewDecision returns the decision record for a transaction if it underwent review adjudication.
func (s *Store) GetReviewDecision(ctx context.Context, shopID, transactionID string) (*ReviewDecision, error) {
	if shopID == "" || transactionID == "" {
		return nil, errors.New("storage: GetReviewDecision requires shop_id and transaction_id")
	}

	const query = `SELECT id, shop_id, transaction_id, decision, decided_by, decided_at, original_values, corrected_values, notes
		FROM review_decisions
		WHERE shop_id = ? AND transaction_id = ?
		ORDER BY decided_at DESC LIMIT 1`

	var (
		dec                                    ReviewDecision
		kind, decidedAtRaw, origJSON, corrJSON string
	)
	err := s.db.QueryRowContext(ctx, query, shopID, transactionID).Scan(
		&dec.ID, &dec.ShopID, &dec.TransactionID, &kind, &dec.DecidedBy, &decidedAtRaw, &origJSON, &corrJSON, &dec.Notes,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage: get review decision: %w", err)
	}

	dec.Decision = ReviewDecisionType(kind)
	if dec.DecidedAt, err = parseTime(decidedAtRaw); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(origJSON), &dec.OriginalValues); err != nil {
		return nil, fmt.Errorf("storage: decode original values: %w", err)
	}
	if corrJSON != "" && corrJSON != "{}" {
		_ = json.Unmarshal([]byte(corrJSON), &dec.CorrectedValues)
	}
	return &dec, nil
}
