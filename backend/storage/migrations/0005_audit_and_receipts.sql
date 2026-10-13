-- 0005_audit_and_receipts.sql — multi-line receipt grouping, product archiving, and review decision audit history.

-- 1. Multi-line receipt grouping: links transaction items originating from a single spoken capture/receipt.
ALTER TABLE transactions ADD COLUMN receipt_id TEXT;
CREATE INDEX IF NOT EXISTS idx_transactions_receipt ON transactions(shop_id, receipt_id);

-- 2. Safe product archiving: soft-deactivates discontinued items without deleting historical movements or transactions.
ALTER TABLE products ADD COLUMN archived_at TEXT;
CREATE INDEX IF NOT EXISTS idx_products_archived ON products(shop_id, archived_at);

-- 3. Review decision audit trail: immutable log of who adjudicated held captures, when, and what values changed.
CREATE TABLE IF NOT EXISTS review_decisions (
    id               TEXT PRIMARY KEY,
    shop_id          TEXT NOT NULL,
    transaction_id   TEXT NOT NULL,
    decision         TEXT NOT NULL, -- 'confirmed', 'rejected', 'corrected_and_confirmed'
    decided_by       TEXT NOT NULL,
    decided_at       TEXT NOT NULL,
    original_values  TEXT NOT NULL, -- JSON: product_id, product_name, quantity, unit, amount_amount, amount_currency
    corrected_values TEXT NOT NULL DEFAULT '{}', -- JSON
    notes            TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (shop_id, transaction_id) REFERENCES transactions(shop_id, id)
);

CREATE INDEX IF NOT EXISTS idx_review_decisions_txn ON review_decisions(shop_id, transaction_id);

