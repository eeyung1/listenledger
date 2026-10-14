-- 0006_target_gates_and_training.sql — cashier protocol training tracking and owner target gates agreement.

-- 1. Track cashier training completion on the shop record.
ALTER TABLE shops ADD COLUMN staff_trained_at TEXT;

-- 2. Objective pilot release gates agreed with the shop owner before trial.
CREATE TABLE IF NOT EXISTS shop_target_gates (
    shop_id                       TEXT PRIMARY KEY REFERENCES shops(id),
    min_full_transaction_accuracy REAL NOT NULL,
    min_sku_accuracy              REAL NOT NULL,
    min_quantity_accuracy         REAL NOT NULL,
    min_unit_accuracy             REAL NOT NULL,
    min_amount_accuracy           REAL NOT NULL,
    max_false_capture_rate        REAL NOT NULL,
    max_missed_transaction_rate   REAL NOT NULL,
    max_correction_rate           REAL NOT NULL,
    min_protocol_compliance_rate  REAL NOT NULL,
    max_p95_latency_ms            REAL NOT NULL,
    agreed_at                     TEXT NOT NULL,
    agreed_by                     TEXT NOT NULL
);

