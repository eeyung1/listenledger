package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TableReconciliation records row count verification for one table.
type TableReconciliation struct {
	TableName string `json:"table_name"`
	SrcCount  int64  `json:"src_count"`
	DstCount  int64  `json:"dst_count"`
	Match     bool   `json:"match"`
}

// ShopReconciliation records per-tenant ledger and inventory invariant verification.
type ShopReconciliation struct {
	ShopID               string `json:"shop_id"`
	ProductCountMatch    bool   `json:"product_count_match"`
	SrcProductCount      int    `json:"src_product_count"`
	DstProductCount      int    `json:"dst_product_count"`
	StockSumMatch        bool   `json:"stock_sum_match"`
	SrcStockSum          int64  `json:"src_stock_sum"`
	DstStockSum          int64  `json:"dst_stock_sum"`
	SalesRevenueMatch    bool   `json:"sales_revenue_match"`
	SrcSalesRevenue      int64  `json:"src_sales_revenue"`
	DstSalesRevenue      int64  `json:"dst_sales_revenue"`
	PendingReviewsMatch  bool   `json:"pending_reviews_match"`
	SrcPendingReviews    int    `json:"src_pending_reviews"`
	DstPendingReviews    int    `json:"dst_pending_reviews"`
	ExpenseSumMatch      bool   `json:"expense_sum_match"`
	SrcExpenseSum        int64  `json:"src_expense_sum"`
	DstExpenseSum        int64  `json:"dst_expense_sum"`
	CustomerDebtSumMatch bool   `json:"customer_debt_sum_match"`
	SrcCustomerDebtSum   int64  `json:"src_customer_debt_sum"`
	DstCustomerDebtSum   int64  `json:"dst_customer_debt_sum"`
}

// ReconciliationReport summarizes the migration verification status.
type ReconciliationReport struct {
	Tables     []TableReconciliation `json:"tables"`
	Shops      []ShopReconciliation  `json:"shops"`
	AllMatched bool                  `json:"all_matched"`
	DryRun     bool                  `json:"dry_run"`
	Duration   time.Duration         `json:"duration"`
}

type tableTransferDef struct {
	name    string
	columns []string
	orderBy string
}

var transferDependencyOrder = []tableTransferDef{
	{
		name:    "owners",
		columns: []string{"id", "email", "password_hash", "recovery_hash", "created_at"},
		orderBy: "created_at ASC, id ASC",
	},
	{
		name:    "shops",
		columns: []string{"id", "owner_id", "name", "currency", "timezone", "confidence_threshold", "sale_phrase", "restock_phrase", "staff_trained_at"},
		orderBy: "id ASC",
	},
	{
		name: "shop_target_gates",
		columns: []string{
			"shop_id",
			"min_full_transaction_accuracy",
			"min_sku_accuracy",
			"min_quantity_accuracy",
			"min_unit_accuracy",
			"min_amount_accuracy",
			"max_false_capture_rate",
			"max_missed_transaction_rate",
			"max_correction_rate",
			"min_protocol_compliance_rate",
			"max_p95_latency_ms",
			"agreed_at",
			"agreed_by",
		},
		orderBy: "shop_id ASC",
	},
	{
		name:    "products",
		columns: []string{"id", "shop_id", "name", "sku", "aliases", "price_amount", "price_currency", "quantity", "base_unit", "packaging", "low_stock_threshold", "created_at", "updated_at", "archived_at"},
		orderBy: "created_at ASC, id ASC",
	},
	{
		name:    "transactions",
		columns: []string{"id", "shop_id", "type", "product_id", "product_name", "quantity", "unit", "amount_amount", "amount_currency", "confidence_overall", "confidence_product", "confidence_quantity", "confidence_amount", "status", "occurred_at", "created_at", "transcript", "receipt_id", "supplier_name", "customer_id", "customer_name", "audio_data"},
		orderBy: "occurred_at ASC, id ASC",
	},
	{
		name:    "stock_movements",
		columns: []string{"transaction_id", "shop_id", "product_id", "delta", "created_at"},
		orderBy: "created_at ASC",
	},
	{
		name:    "ledger_requests",
		columns: []string{"shop_id", "request_key", "payload_hash", "transaction_id"},
		orderBy: "shop_id ASC, request_key ASC",
	},
	{
		name:    "devices",
		columns: []string{"id", "shop_id", "name", "token_hash", "paired_at", "revoked_at"},
		orderBy: "paired_at ASC, id ASC",
	},
	{
		name:    "review_decisions",
		columns: []string{"id", "shop_id", "transaction_id", "decision", "decided_by", "decided_at", "original_values", "corrected_values", "notes"},
		orderBy: "decided_at ASC, id ASC",
	},
	{
		name:    "sessions",
		columns: []string{"token_hash", "owner_id", "expires_at"},
		orderBy: "expires_at ASC",
	},
	{
		name:    "auth_attempts",
		columns: []string{"key", "attempts", "expires_at"},
		orderBy: "key ASC",
	},
	{
		name: "pilot_reconciliations",
		columns: []string{
			"id", "shop_id", "status", "period_start", "period_end", "independent_source",
			"total_independent_records", "total_captured_transactions", "matched_records",
			"missed_captures", "false_captures", "price_discrepancies", "quantity_discrepancies",
			"total_independent_revenue", "total_captured_revenue", "revenue_discrepancy",
			"stock_discrepancy_count", "notes", "conducted_by", "created_at",
		},
		orderBy: "created_at ASC, id ASC",
	},
	{
		name: "pilot_discrepancies",
		columns: []string{
			"id", "reconciliation_id", "shop_id", "discrepancy_type", "product_id",
			"product_name", "expected_quantity", "actual_quantity", "expected_amount",
			"actual_amount", "transaction_id", "investigation_status", "root_cause",
			"investigation_notes", "investigated_at", "investigated_by",
		},
		orderBy: "id ASC",
	},
	{
		name: "shop_cohorts",
		columns: []string{
			"shop_id", "cohort_phase", "stage_status", "qualified_at", "notes",
		},
		orderBy: "shop_id ASC",
	},
	{
		name: "expenses",
		columns: []string{
			"id", "shop_id", "category", "amount_amount", "amount_currency",
			"description", "receipt_ref", "occurred_at", "created_by", "created_at",
		},
		orderBy: "occurred_at ASC, id ASC",
	},
	{
		name: "customers",
		columns: []string{
			"id", "shop_id", "name", "phone", "notes",
			"outstanding_amount", "outstanding_currency", "created_at", "updated_at",
		},
		orderBy: "created_at ASC, id ASC",
	},
	{
		name: "customer_debts",
		columns: []string{
			"id", "shop_id", "customer_id", "type", "amount_amount", "amount_currency",
			"balance_after_amount", "balance_after_currency", "description", "receipt_ref",
			"occurred_at", "created_by", "created_at",
		},
		orderBy: "occurred_at ASC, id ASC",
	},
}

// Transfer copies all persistent records from src to dst in dependency order without
// replaying transactions as new stock movements. It verifies all table counts and
// per-tenant invariants (product counts, current stock totals, confirmed sales revenue,
// and pending review counts). If dryRun is true, the destination transaction rolls back.
func Transfer(ctx context.Context, src, dst *Store, dryRun bool) (ReconciliationReport, error) {
	if src == nil || src.db == nil {
		return ReconciliationReport{}, errors.New("storage: source store is uninitialized")
	}
	if dst == nil || dst.db == nil {
		return ReconciliationReport{}, errors.New("storage: destination store is uninitialized")
	}

	start := time.Now()
	report := ReconciliationReport{
		Tables:     make([]TableReconciliation, 0, len(transferDependencyOrder)),
		Shops:      make([]ShopReconciliation, 0),
		DryRun:     dryRun,
		AllMatched: true,
	}

	tx, err := dst.db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("storage: begin destination transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// 1. Transfer records in dependency order
	for _, def := range transferDependencyOrder {
		colList := strings.Join(def.columns, ", ")
		srcQuery := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", colList, def.name, def.orderBy)
		rows, err := src.db.QueryContext(ctx, srcQuery)
		if err != nil {
			return report, fmt.Errorf("storage: read %s from source: %w", def.name, err)
		}

		placeholders := make([]string, len(def.columns))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		rawInsert := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			def.name, colList, strings.Join(placeholders, ", "))
		dstInsert := dst.rebind(rawInsert)

		stmt, err := tx.PrepareContext(ctx, dstInsert)
		if err != nil {
			rows.Close()
			return report, fmt.Errorf("storage: prepare insert for %s: %w", def.name, err)
		}

		var count int64
		colPointers := make([]any, len(def.columns))
		for rows.Next() {
			rowVals := make([]any, len(def.columns))
			for i := range rowVals {
				colPointers[i] = &rowVals[i]
			}
			if err := rows.Scan(colPointers...); err != nil {
				stmt.Close()
				rows.Close()
				return report, fmt.Errorf("storage: scan %s from source: %w", def.name, err)
			}

			// Convert []byte text columns to string if needed
			for i, v := range rowVals {
				if b, ok := v.([]byte); ok {
					rowVals[i] = string(b)
				}
			}

			if _, err := stmt.ExecContext(ctx, rowVals...); err != nil {
				stmt.Close()
				rows.Close()
				return report, fmt.Errorf("storage: insert into %s: %w", def.name, err)
			}
			count++
		}
		stmt.Close()
		if err := rows.Err(); err != nil {
			rows.Close()
			return report, fmt.Errorf("storage: iterate %s: %w", def.name, err)
		}
		rows.Close()

		var dstCount int64
		countQuery := dst.rebind(fmt.Sprintf("SELECT COUNT(*) FROM %s", def.name))
		if err := tx.QueryRowContext(ctx, countQuery).Scan(&dstCount); err != nil {
			return report, fmt.Errorf("storage: count destination %s: %w", def.name, err)
		}

		match := (count == dstCount)
		if !match {
			report.AllMatched = false
		}
		report.Tables = append(report.Tables, TableReconciliation{
			TableName: def.name,
			SrcCount:  count,
			DstCount:  dstCount,
			Match:     match,
		})
	}

	// 2. Tenant invariant reconciliation across all migrated shops
	shopRows, err := src.db.QueryContext(ctx, `SELECT id FROM shops ORDER BY id ASC`)
	if err != nil {
		return report, fmt.Errorf("storage: query source shops for reconciliation: %w", err)
	}
	defer shopRows.Close()

	for shopRows.Next() {
		var shopID string
		if err := shopRows.Scan(&shopID); err != nil {
			return report, fmt.Errorf("storage: scan shop id: %w", err)
		}

		sr := ShopReconciliation{ShopID: shopID}

		// Product counts
		_ = src.db.QueryRowContext(ctx, src.rebind(`SELECT COUNT(*) FROM products WHERE shop_id = ?`), shopID).Scan(&sr.SrcProductCount)
		_ = tx.QueryRowContext(ctx, dst.rebind(`SELECT COUNT(*) FROM products WHERE shop_id = ?`), shopID).Scan(&sr.DstProductCount)
		sr.ProductCountMatch = (sr.SrcProductCount == sr.DstProductCount)

		// Stock sums
		_ = src.db.QueryRowContext(ctx, src.rebind(`SELECT COALESCE(SUM(quantity), 0) FROM products WHERE shop_id = ?`), shopID).Scan(&sr.SrcStockSum)
		_ = tx.QueryRowContext(ctx, dst.rebind(`SELECT COALESCE(SUM(quantity), 0) FROM products WHERE shop_id = ?`), shopID).Scan(&sr.DstStockSum)
		sr.StockSumMatch = (sr.SrcStockSum == sr.DstStockSum)

		// Sales revenue
		const salesQuery = `SELECT COALESCE(SUM(amount_amount), 0) FROM transactions WHERE shop_id = ? AND type = 'sale' AND status = 'confirmed'`
		_ = src.db.QueryRowContext(ctx, src.rebind(salesQuery), shopID).Scan(&sr.SrcSalesRevenue)
		_ = tx.QueryRowContext(ctx, dst.rebind(salesQuery), shopID).Scan(&sr.DstSalesRevenue)
		sr.SalesRevenueMatch = (sr.SrcSalesRevenue == sr.DstSalesRevenue)

		// Pending reviews
		const reviewsQuery = `SELECT COUNT(*) FROM transactions WHERE shop_id = ? AND status = 'review_required'`
		_ = src.db.QueryRowContext(ctx, src.rebind(reviewsQuery), shopID).Scan(&sr.SrcPendingReviews)
		_ = tx.QueryRowContext(ctx, dst.rebind(reviewsQuery), shopID).Scan(&sr.DstPendingReviews)
		sr.PendingReviewsMatch = (sr.SrcPendingReviews == sr.DstPendingReviews)

		// Operating expenses sum
		const expenseQuery = `SELECT COALESCE(SUM(amount_amount), 0) FROM expenses WHERE shop_id = ?`
		_ = src.db.QueryRowContext(ctx, src.rebind(expenseQuery), shopID).Scan(&sr.SrcExpenseSum)
		_ = tx.QueryRowContext(ctx, dst.rebind(expenseQuery), shopID).Scan(&sr.DstExpenseSum)
		sr.ExpenseSumMatch = (sr.SrcExpenseSum == sr.DstExpenseSum)

		// Customer outstanding debts sum
		const customerDebtQuery = `SELECT COALESCE(SUM(outstanding_amount), 0) FROM customers WHERE shop_id = ?`
		_ = src.db.QueryRowContext(ctx, src.rebind(customerDebtQuery), shopID).Scan(&sr.SrcCustomerDebtSum)
		_ = tx.QueryRowContext(ctx, dst.rebind(customerDebtQuery), shopID).Scan(&sr.DstCustomerDebtSum)
		sr.CustomerDebtSumMatch = (sr.SrcCustomerDebtSum == sr.DstCustomerDebtSum)

		if !sr.ProductCountMatch || !sr.StockSumMatch || !sr.SalesRevenueMatch || !sr.PendingReviewsMatch || !sr.ExpenseSumMatch || !sr.CustomerDebtSumMatch {
			report.AllMatched = false
		}
		report.Shops = append(report.Shops, sr)
	}

	if dryRun {
		report.Duration = time.Since(start)
		return report, nil
	}

	if !report.AllMatched {
		return report, errors.New("storage: reconciliation mismatch detected between source and destination")
	}

	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("storage: commit destination migration transaction: %w", err)
	}

	report.Duration = time.Since(start)
	return report, nil
}
