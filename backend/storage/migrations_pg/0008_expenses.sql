-- 0008_expenses.sql — Shop operational expense tracking, category classification, and profitability accounting.

CREATE TABLE IF NOT EXISTS expenses (
    id              TEXT PRIMARY KEY,
    shop_id         TEXT NOT NULL REFERENCES shops(id),
    category        TEXT NOT NULL CHECK (category IN (
        'generator_fuel',
        'store_rent',
        'transport_logistics',
        'packaging_supplies',
        'staff_wages',
        'utilities',
        'repairs',
        'other'
    )),
    amount_amount   BIGINT NOT NULL CHECK (amount_amount > 0),
    amount_currency TEXT NOT NULL,
    description     TEXT NOT NULL,
    receipt_ref     TEXT NOT NULL DEFAULT '',
    occurred_at     TEXT NOT NULL,
    created_by      TEXT NOT NULL DEFAULT 'owner',
    created_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_expenses_shop_occurred
    ON expenses (shop_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_expenses_shop_category
    ON expenses (shop_id, category);

