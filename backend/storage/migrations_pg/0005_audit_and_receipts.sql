-- 0005_audit_and_receipts.sql (PostgreSQL) — multi-line receipt grouping, product archiving, and review decision audit history.

ALTER TABLE transactions ADD COLUMN IF NOT EXISTS receipt_id TEXT;
CREATE INDEX IF NOT EXISTS idx_transactions_receipt ON transactions(shop_id, receipt_id);

ALTER TABLE products ADD COLUMN IF NOT EXISTS archived_at TEXT;
CREATE INDEX IF NOT EXISTS idx_products_archived ON products(shop_id, archived_at);

CREATE TABLE IF NOT EXISTS review_decisions (
    id               TEXT PRIMARY KEY,
    shop_id          TEXT NOT NULL,
    transaction_id   TEXT NOT NULL,
    decision         TEXT NOT NULL,
    decided_by       TEXT NOT NULL,
    decided_at       TEXT NOT NULL,
    original_values  TEXT NOT NULL,
    corrected_values TEXT NOT NULL DEFAULT '{}',
    notes            TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (shop_id, transaction_id) REFERENCES transactions(shop_id, id)
);

CREATE INDEX IF NOT EXISTS idx_review_decisions_txn ON review_decisions(shop_id, transaction_id);

