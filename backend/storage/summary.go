package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

// DailySummary carries server-authoritative daily aggregates for the dashboard KPIs.
type DailySummary struct {
	ShopID           string       `json:"shop_id"`
	Currency         string       `json:"currency"`
	TodaySalesAmount models.Money `json:"today_sales_amount"`
	TodaySalesCount  int          `json:"today_sales_count"`
	PendingReviews   int          `json:"pending_reviews"`
	LowStockCount    int          `json:"low_stock_count"`
	LastSeq          int64        `json:"last_seq"`
	GeneratedAt      time.Time    `json:"generated_at"`
}

// GetDailySummary computes authoritative totals for today's trading day within
// the shop's local timezone.
func (s *Store) GetDailySummary(ctx context.Context, shopID string, startUTC, endUTC time.Time) (DailySummary, error) {
	if shopID == "" {
		return DailySummary{}, errors.New("storage: shop_id is required")
	}

	// 1. Fetch shop currency. Default to NGN if unconfigured.
	var currency string
	err := s.db.QueryRowContext(ctx, `SELECT currency FROM shops WHERE id = ?`, shopID).Scan(&currency)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DailySummary{}, fmt.Errorf("storage: get shop currency: %w", err)
	}
	if currency == "" {
		currency = "NGN"
	}

	// 2. Compute confirmed sales total and count within the today window.
	var salesSum int64
	var salesCount int
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_amount), 0), COUNT(id)
		FROM transactions
		WHERE shop_id = ?
		  AND type = 'sale'
		  AND status = 'confirmed'
		  AND occurred_at >= ?
		  AND occurred_at < ?
	`, shopID, formatTime(startUTC), formatTime(endUTC)).Scan(&salesSum, &salesCount)
	if err != nil {
		return DailySummary{}, fmt.Errorf("storage: query sales summary: %w", err)
	}

	// 3. Count held transactions awaiting human review.
	var pendingReviews int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(id)
		FROM transactions
		WHERE shop_id = ?
		  AND status = 'review_required'
	`, shopID).Scan(&pendingReviews)
	if err != nil {
		return DailySummary{}, fmt.Errorf("storage: query review count: %w", err)
	}

	// 4. Count products at or below their reorder threshold.
	var lowStockCount int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(id)
		FROM products
		WHERE shop_id = ?
		  AND quantity <= low_stock_threshold
	`, shopID).Scan(&lowStockCount)
	if err != nil {
		return DailySummary{}, fmt.Errorf("storage: query low stock count: %w", err)
	}

	return DailySummary{
		ShopID:   shopID,
		Currency: currency,
		TodaySalesAmount: models.Money{
			Amount:   salesSum,
			Currency: currency,
		},
		TodaySalesCount: salesCount,
		PendingReviews:  pendingReviews,
		LowStockCount:   lowStockCount,
		GeneratedAt:     time.Now().UTC(),
	}, nil
}
