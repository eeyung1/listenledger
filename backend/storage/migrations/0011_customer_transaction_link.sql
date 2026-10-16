-- 0011_customer_transaction_link.sql — Spoken customer credit book attribution.

ALTER TABLE transactions ADD COLUMN customer_id TEXT NOT NULL DEFAULT '';
ALTER TABLE transactions ADD COLUMN customer_name TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_transactions_customer ON transactions (shop_id, customer_id);

