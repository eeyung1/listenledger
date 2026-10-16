-- 0010_supplier_and_cost.sql — Spoken supplier attribution for inventory restocks.

ALTER TABLE transactions ADD COLUMN IF NOT EXISTS supplier_name TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_transactions_supplier ON transactions (shop_id, supplier_name);

