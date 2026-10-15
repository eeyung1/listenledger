-- 0009_customer_debts.sql — Customer credit and debt ledger (I-Owe-You / customer book) tracking.

CREATE TABLE IF NOT EXISTS customers (
    id                   TEXT PRIMARY KEY,
    shop_id              TEXT NOT NULL REFERENCES shops(id),
    name                 TEXT NOT NULL,
    phone                TEXT NOT NULL DEFAULT '',
    notes                TEXT NOT NULL DEFAULT '',
    outstanding_amount   BIGINT NOT NULL DEFAULT 0,
    outstanding_currency TEXT NOT NULL,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_customers_shop_name
    ON customers (shop_id, name);

CREATE INDEX IF NOT EXISTS idx_customers_shop_balance
    ON customers (shop_id, outstanding_amount DESC);

CREATE TABLE IF NOT EXISTS customer_debts (
    id                     TEXT PRIMARY KEY,
    shop_id                TEXT NOT NULL REFERENCES shops(id),
    customer_id            TEXT NOT NULL REFERENCES customers(id),
    type                   TEXT NOT NULL CHECK (type IN ('credit_sale', 'repayment', 'forgiveness')),
    amount_amount          BIGINT NOT NULL CHECK (amount_amount > 0),
    amount_currency        TEXT NOT NULL,
    balance_after_amount   BIGINT NOT NULL,
    balance_after_currency TEXT NOT NULL,
    description            TEXT NOT NULL,
    receipt_ref            TEXT NOT NULL DEFAULT '',
    occurred_at            TEXT NOT NULL,
    created_by             TEXT NOT NULL DEFAULT 'owner',
    created_at             TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_customer_debts_shop_customer
    ON customer_debts (shop_id, customer_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_customer_debts_shop_occurred
    ON customer_debts (shop_id, occurred_at DESC);

