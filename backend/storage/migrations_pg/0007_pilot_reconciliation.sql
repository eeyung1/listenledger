-- 0007_pilot_reconciliation.sql — Independent paper ledger reconciliation, discrepancy auditing, and cohort progression.

CREATE TABLE IF NOT EXISTS pilot_reconciliations (
    id                           TEXT PRIMARY KEY,
    shop_id                      TEXT NOT NULL REFERENCES shops(id),
    status                       TEXT NOT NULL CHECK (status IN ('in_progress', 'completed', 'discrepant', 'reconciled')),
    period_start                 TEXT NOT NULL,
    period_end                   TEXT NOT NULL,
    independent_source           TEXT NOT NULL,
    total_independent_records    INTEGER NOT NULL DEFAULT 0,
    total_captured_transactions  INTEGER NOT NULL DEFAULT 0,
    matched_records              INTEGER NOT NULL DEFAULT 0,
    missed_captures              INTEGER NOT NULL DEFAULT 0,
    false_captures               INTEGER NOT NULL DEFAULT 0,
    price_discrepancies          INTEGER NOT NULL DEFAULT 0,
    quantity_discrepancies       INTEGER NOT NULL DEFAULT 0,
    total_independent_revenue   BIGINT NOT NULL DEFAULT 0,
    total_captured_revenue      BIGINT NOT NULL DEFAULT 0,
    revenue_discrepancy          BIGINT NOT NULL DEFAULT 0,
    stock_discrepancy_count      INTEGER NOT NULL DEFAULT 0,
    notes                        TEXT NOT NULL DEFAULT '',
    conducted_by                 TEXT NOT NULL,
    created_at                   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pilot_reconciliations_shop_created
    ON pilot_reconciliations (shop_id, created_at DESC);

CREATE TABLE IF NOT EXISTS pilot_discrepancies (
    id                    TEXT PRIMARY KEY,
    reconciliation_id     TEXT NOT NULL REFERENCES pilot_reconciliations(id) ON DELETE CASCADE,
    shop_id               TEXT NOT NULL REFERENCES shops(id),
    discrepancy_type      TEXT NOT NULL CHECK (discrepancy_type IN ('missed_capture', 'false_capture', 'price_mismatch', 'quantity_mismatch', 'stock_variance')),
    product_id            TEXT REFERENCES products(id),
    product_name          TEXT NOT NULL,
    expected_quantity     INTEGER NOT NULL DEFAULT 0,
    actual_quantity       INTEGER NOT NULL DEFAULT 0,
    expected_amount       BIGINT NOT NULL DEFAULT 0,
    actual_amount         BIGINT NOT NULL DEFAULT 0,
    transaction_id        TEXT REFERENCES transactions(id),
    investigation_status  TEXT NOT NULL DEFAULT 'unexplained' CHECK (investigation_status IN ('unexplained', 'investigated', 'resolved')),
    root_cause            TEXT NOT NULL DEFAULT '' CHECK (root_cause IN ('', 'acoustic_noise', 'speech_boundary_miss', 'cashier_protocol_drift', 'customer_cancellation', 'physical_shrinkage', 'supplier_shortage', 'other')),
    investigation_notes   TEXT NOT NULL DEFAULT '',
    investigated_at       TEXT,
    investigated_by       TEXT
);

CREATE INDEX IF NOT EXISTS idx_pilot_discrepancies_rec_id
    ON pilot_discrepancies (reconciliation_id);

CREATE INDEX IF NOT EXISTS idx_pilot_discrepancies_shop_status
    ON pilot_discrepancies (shop_id, investigation_status);

CREATE TABLE IF NOT EXISTS shop_cohorts (
    shop_id        TEXT PRIMARY KEY REFERENCES shops(id),
    cohort_phase   TEXT NOT NULL DEFAULT 'phase1_controlled' CHECK (cohort_phase IN ('phase1_controlled', 'phase2_dual_shop', 'phase3_expanded_5', 'phase4_scaled_15')),
    stage_status   TEXT NOT NULL DEFAULT 'active' CHECK (stage_status IN ('active', 'qualified', 'blocked')),
    qualified_at   TEXT,
    notes          TEXT NOT NULL DEFAULT ''
);

