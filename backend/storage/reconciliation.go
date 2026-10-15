package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PilotReconciliation represents a dual-entry reconciliation session.
type PilotReconciliation struct {
	ID                        string             `json:"id"`
	ShopID                    string             `json:"shop_id"`
	Status                    string             `json:"status"` // 'in_progress', 'completed', 'discrepant', 'reconciled'
	PeriodStart               time.Time          `json:"period_start"`
	PeriodEnd                 time.Time          `json:"period_end"`
	IndependentSource         string             `json:"independent_source"`
	TotalIndependentRecords   int                `json:"total_independent_records"`
	TotalCapturedTransactions int                `json:"total_captured_transactions"`
	MatchedRecords            int                `json:"matched_records"`
	MissedCaptures            int                `json:"missed_captures"`
	FalseCaptures             int                `json:"false_captures"`
	PriceDiscrepancies        int                `json:"price_discrepancies"`
	QuantityDiscrepancies     int                `json:"quantity_discrepancies"`
	TotalIndependentRevenue   int64              `json:"total_independent_revenue"`
	TotalCapturedRevenue      int64              `json:"total_captured_revenue"`
	RevenueDiscrepancy        int64              `json:"revenue_discrepancy"`
	StockDiscrepancyCount     int                `json:"stock_discrepancy_count"`
	Notes                     string             `json:"notes"`
	ConductedBy               string             `json:"conducted_by"`
	CreatedAt                 time.Time          `json:"created_at"`
	Discrepancies             []PilotDiscrepancy `json:"discrepancies,omitempty"`
}

// PilotDiscrepancy represents an unexplained difference between independent ground-truth and ambient capture.
type PilotDiscrepancy struct {
	ID                  string     `json:"id"`
	ReconciliationID    string     `json:"reconciliation_id"`
	ShopID              string     `json:"shop_id"`
	DiscrepancyType     string     `json:"discrepancy_type"` // 'missed_capture', 'false_capture', 'price_mismatch', 'quantity_mismatch', 'stock_variance'
	ProductID           *string    `json:"product_id,omitempty"`
	ProductName         string     `json:"product_name"`
	ExpectedQuantity    int        `json:"expected_quantity"`
	ActualQuantity      int        `json:"actual_quantity"`
	ExpectedAmount      int64      `json:"expected_amount"`
	ActualAmount        int64      `json:"actual_amount"`
	TransactionID       *string    `json:"transaction_id,omitempty"`
	InvestigationStatus string     `json:"investigation_status"` // 'unexplained', 'investigated', 'resolved'
	RootCause           string     `json:"root_cause"`           // 'acoustic_noise', 'speech_boundary_miss', 'cashier_protocol_drift', etc.
	InvestigationNotes  string     `json:"investigation_notes"`
	InvestigatedAt      *time.Time `json:"investigated_at,omitempty"`
	InvestigatedBy      *string    `json:"investigated_by,omitempty"`
}

// ShopCohort represents a shop's progression through pilot trial cohort phases.
type ShopCohort struct {
	ShopID      string     `json:"shop_id"`
	CohortPhase string     `json:"cohort_phase"` // 'phase1_controlled', 'phase2_dual_shop', 'phase3_expanded_5', 'phase4_scaled_15'
	StageStatus string     `json:"stage_status"` // 'active', 'qualified', 'blocked'
	QualifiedAt *time.Time `json:"qualified_at,omitempty"`
	Notes       string     `json:"notes"`
}

// CreateReconciliation records a new reconciliation session and its line discrepancies in one transaction.
func (s *Store) CreateReconciliation(ctx context.Context, rec PilotReconciliation, discrepancies []PilotDiscrepancy) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin reconciliation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const insertRec = `
		INSERT INTO pilot_reconciliations (
			id, shop_id, status, period_start, period_end, independent_source,
			total_independent_records, total_captured_transactions, matched_records,
			missed_captures, false_captures, price_discrepancies, quantity_discrepancies,
			total_independent_revenue, total_captured_revenue, revenue_discrepancy,
			stock_discrepancy_count, notes, conducted_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = tx.ExecContext(ctx, s.rebind(insertRec),
		rec.ID,
		rec.ShopID,
		rec.Status,
		rec.PeriodStart.UTC().Format(timeLayout),
		rec.PeriodEnd.UTC().Format(timeLayout),
		rec.IndependentSource,
		rec.TotalIndependentRecords,
		rec.TotalCapturedTransactions,
		rec.MatchedRecords,
		rec.MissedCaptures,
		rec.FalseCaptures,
		rec.PriceDiscrepancies,
		rec.QuantityDiscrepancies,
		rec.TotalIndependentRevenue,
		rec.TotalCapturedRevenue,
		rec.RevenueDiscrepancy,
		rec.StockDiscrepancyCount,
		rec.Notes,
		rec.ConductedBy,
		rec.CreatedAt.UTC().Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("storage: insert reconciliation: %w", err)
	}

	const insertDisc = `
		INSERT INTO pilot_discrepancies (
			id, reconciliation_id, shop_id, discrepancy_type, product_id,
			product_name, expected_quantity, actual_quantity, expected_amount,
			actual_amount, transaction_id, investigation_status, root_cause,
			investigation_notes, investigated_at, investigated_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	for _, d := range discrepancies {
		var invAt *string
		if d.InvestigatedAt != nil {
			tStr := d.InvestigatedAt.UTC().Format(timeLayout)
			invAt = &tStr
		}

		_, err = tx.ExecContext(ctx, s.rebind(insertDisc),
			d.ID,
			rec.ID,
			rec.ShopID,
			d.DiscrepancyType,
			d.ProductID,
			d.ProductName,
			d.ExpectedQuantity,
			d.ActualQuantity,
			d.ExpectedAmount,
			d.ActualAmount,
			d.TransactionID,
			d.InvestigationStatus,
			d.RootCause,
			d.InvestigationNotes,
			invAt,
			d.InvestigatedBy,
		)
		if err != nil {
			return fmt.Errorf("storage: insert discrepancy %s: %w", d.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit reconciliation: %w", err)
	}
	return nil
}

// GetReconciliation returns a reconciliation and all associated discrepancies for a shop.
func (s *Store) GetReconciliation(ctx context.Context, shopID, id string) (PilotReconciliation, error) {
	const queryRec = `
		SELECT
			id, shop_id, status, period_start, period_end, independent_source,
			total_independent_records, total_captured_transactions, matched_records,
			missed_captures, false_captures, price_discrepancies, quantity_discrepancies,
			total_independent_revenue, total_captured_revenue, revenue_discrepancy,
			stock_discrepancy_count, notes, conducted_by, created_at
		FROM pilot_reconciliations
		WHERE shop_id = ? AND id = ?`

	var rec PilotReconciliation
	var startStr, endStr, createdStr string

	err := s.db.QueryRowContext(ctx, s.rebind(queryRec), shopID, id).Scan(
		&rec.ID,
		&rec.ShopID,
		&rec.Status,
		&startStr,
		&endStr,
		&rec.IndependentSource,
		&rec.TotalIndependentRecords,
		&rec.TotalCapturedTransactions,
		&rec.MatchedRecords,
		&rec.MissedCaptures,
		&rec.FalseCaptures,
		&rec.PriceDiscrepancies,
		&rec.QuantityDiscrepancies,
		&rec.TotalIndependentRevenue,
		&rec.TotalCapturedRevenue,
		&rec.RevenueDiscrepancy,
		&rec.StockDiscrepancyCount,
		&rec.Notes,
		&rec.ConductedBy,
		&createdStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PilotReconciliation{}, ErrNotFound
	}
	if err != nil {
		return PilotReconciliation{}, fmt.Errorf("storage: get reconciliation: %w", err)
	}

	rec.PeriodStart, _ = time.Parse(timeLayout, startStr)
	rec.PeriodEnd, _ = time.Parse(timeLayout, endStr)
	rec.CreatedAt, _ = time.Parse(timeLayout, createdStr)

	// Fetch discrepancies
	const queryDisc = `
		SELECT
			id, reconciliation_id, shop_id, discrepancy_type, product_id,
			product_name, expected_quantity, actual_quantity, expected_amount,
			actual_amount, transaction_id, investigation_status, root_cause,
			investigation_notes, investigated_at, investigated_by
		FROM pilot_discrepancies
		WHERE shop_id = ? AND reconciliation_id = ?
		ORDER BY id ASC`

	rows, err := s.db.QueryContext(ctx, s.rebind(queryDisc), shopID, id)
	if err != nil {
		return PilotReconciliation{}, fmt.Errorf("storage: get discrepancies: %w", err)
	}
	defer rows.Close()

	var discrepancies []PilotDiscrepancy
	for rows.Next() {
		var d PilotDiscrepancy
		var invAtStr sql.NullString
		err := rows.Scan(
			&d.ID,
			&d.ReconciliationID,
			&d.ShopID,
			&d.DiscrepancyType,
			&d.ProductID,
			&d.ProductName,
			&d.ExpectedQuantity,
			&d.ActualQuantity,
			&d.ExpectedAmount,
			&d.ActualAmount,
			&d.TransactionID,
			&d.InvestigationStatus,
			&d.RootCause,
			&d.InvestigationNotes,
			&invAtStr,
			&d.InvestigatedBy,
		)
		if err != nil {
			return PilotReconciliation{}, fmt.Errorf("storage: scan discrepancy: %w", err)
		}
		if invAtStr.Valid && invAtStr.String != "" {
			t, err := time.Parse(timeLayout, invAtStr.String)
			if err == nil {
				d.InvestigatedAt = &t
			}
		}
		discrepancies = append(discrepancies, d)
	}
	if discrepancies == nil {
		discrepancies = []PilotDiscrepancy{}
	}
	rec.Discrepancies = discrepancies

	return rec, nil
}

// ListReconciliations returns the list of historical reconciliation sessions for a shop.
func (s *Store) ListReconciliations(ctx context.Context, shopID string, limit, offset int) ([]PilotReconciliation, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	const query = `
		SELECT
			id, shop_id, status, period_start, period_end, independent_source,
			total_independent_records, total_captured_transactions, matched_records,
			missed_captures, false_captures, price_discrepancies, quantity_discrepancies,
			total_independent_revenue, total_captured_revenue, revenue_discrepancy,
			stock_discrepancy_count, notes, conducted_by, created_at
		FROM pilot_reconciliations
		WHERE shop_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ? OFFSET ?`

	rows, err := s.db.QueryContext(ctx, s.rebind(query), shopID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("storage: list reconciliations: %w", err)
	}
	defer rows.Close()

	var list []PilotReconciliation
	for rows.Next() {
		var rec PilotReconciliation
		var startStr, endStr, createdStr string
		err := rows.Scan(
			&rec.ID,
			&rec.ShopID,
			&rec.Status,
			&startStr,
			&endStr,
			&rec.IndependentSource,
			&rec.TotalIndependentRecords,
			&rec.TotalCapturedTransactions,
			&rec.MatchedRecords,
			&rec.MissedCaptures,
			&rec.FalseCaptures,
			&rec.PriceDiscrepancies,
			&rec.QuantityDiscrepancies,
			&rec.TotalIndependentRevenue,
			&rec.TotalCapturedRevenue,
			&rec.RevenueDiscrepancy,
			&rec.StockDiscrepancyCount,
			&rec.Notes,
			&rec.ConductedBy,
			&createdStr,
		)
		if err != nil {
			return nil, fmt.Errorf("storage: scan reconciliation: %w", err)
		}
		rec.PeriodStart, _ = time.Parse(timeLayout, startStr)
		rec.PeriodEnd, _ = time.Parse(timeLayout, endStr)
		rec.CreatedAt, _ = time.Parse(timeLayout, createdStr)
		list = append(list, rec)
	}
	if list == nil {
		list = []PilotReconciliation{}
	}
	return list, nil
}

// UpdateDiscrepancyInvestigation records investigation findings for a discrepancy and updates session status if all resolved.
func (s *Store) UpdateDiscrepancyInvestigation(ctx context.Context, shopID, discrepancyID, rootCause, notes, investigatedBy string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin discrepancy investigation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Get discrepancy to find reconciliation_id
	const getDisc = `SELECT reconciliation_id FROM pilot_discrepancies WHERE shop_id = ? AND id = ?`
	var recID string
	err = tx.QueryRowContext(ctx, s.rebind(getDisc), shopID, discrepancyID).Scan(&recID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("storage: query discrepancy: %w", err)
	}

	// 2. Update discrepancy
	const updateDisc = `
		UPDATE pilot_discrepancies
		SET investigation_status = 'investigated',
		    root_cause = ?,
		    investigation_notes = ?,
		    investigated_at = ?,
		    investigated_by = ?
		WHERE shop_id = ? AND id = ?`

	res, err := tx.ExecContext(ctx, s.rebind(updateDisc),
		rootCause,
		notes,
		now.UTC().Format(timeLayout),
		investigatedBy,
		shopID,
		discrepancyID,
	)
	if err != nil {
		return fmt.Errorf("storage: update discrepancy: %w", err)
	}
	rowsAff, _ := res.RowsAffected()
	if rowsAff == 0 {
		return ErrNotFound
	}

	// 3. Check remaining unexplained discrepancies for this reconciliation
	const countUnexplained = `
		SELECT COUNT(*)
		FROM pilot_discrepancies
		WHERE reconciliation_id = ? AND investigation_status = 'unexplained'`

	var remainingUnexplained int
	if err := tx.QueryRowContext(ctx, s.rebind(countUnexplained), recID).Scan(&remainingUnexplained); err != nil {
		return fmt.Errorf("storage: count unexplained discrepancies: %w", err)
	}

	// If all discrepancies are now investigated and explained, mark reconciliation as 'reconciled'
	if remainingUnexplained == 0 {
		const updateRecStatus = `
			UPDATE pilot_reconciliations
			SET status = 'reconciled'
			WHERE id = ? AND shop_id = ?`
		if _, err := tx.ExecContext(ctx, s.rebind(updateRecStatus), recID, shopID); err != nil {
			return fmt.Errorf("storage: update reconciliation status to reconciled: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit discrepancy investigation: %w", err)
	}
	return nil
}

// GetShopCohort returns the cohort stage and status for a shop.
func (s *Store) GetShopCohort(ctx context.Context, shopID string) (ShopCohort, error) {
	const query = `SELECT shop_id, cohort_phase, stage_status, qualified_at, notes FROM shop_cohorts WHERE shop_id = ?`

	var c ShopCohort
	var qualStr sql.NullString
	err := s.db.QueryRowContext(ctx, s.rebind(query), shopID).Scan(
		&c.ShopID,
		&c.CohortPhase,
		&c.StageStatus,
		&qualStr,
		&c.Notes,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// Default cohort for any shop
		return ShopCohort{
			ShopID:      shopID,
			CohortPhase: "phase1_controlled",
			StageStatus: "active",
			Notes:       "Initial single-shop controlled trial phase.",
		}, nil
	}
	if err != nil {
		return ShopCohort{}, fmt.Errorf("storage: get shop cohort: %w", err)
	}
	if qualStr.Valid && qualStr.String != "" {
		t, err := time.Parse(timeLayout, qualStr.String)
		if err == nil {
			c.QualifiedAt = &t
		}
	}
	return c, nil
}

// UpsertShopCohort updates or creates cohort progression for a shop.
func (s *Store) UpsertShopCohort(ctx context.Context, cohort ShopCohort) error {
	var qualStr *string
	if cohort.QualifiedAt != nil {
		t := cohort.QualifiedAt.UTC().Format(timeLayout)
		qualStr = &t
	}

	var query string
	if s.Dialect().IsPostgres() {
		query = `
			INSERT INTO shop_cohorts (shop_id, cohort_phase, stage_status, qualified_at, notes)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (shop_id) DO UPDATE SET
				cohort_phase = EXCLUDED.cohort_phase,
				stage_status = EXCLUDED.stage_status,
				qualified_at = EXCLUDED.qualified_at,
				notes = EXCLUDED.notes`
	} else {
		query = `
			INSERT INTO shop_cohorts (shop_id, cohort_phase, stage_status, qualified_at, notes)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(shop_id) DO UPDATE SET
				cohort_phase = excluded.cohort_phase,
				stage_status = excluded.stage_status,
				qualified_at = excluded.qualified_at,
				notes = excluded.notes`
	}

	_, err := s.db.ExecContext(ctx, s.rebind(query),
		cohort.ShopID,
		cohort.CohortPhase,
		cohort.StageStatus,
		qualStr,
		cohort.Notes,
	)
	if err != nil {
		return fmt.Errorf("storage: upsert shop cohort: %w", err)
	}
	return nil
}
