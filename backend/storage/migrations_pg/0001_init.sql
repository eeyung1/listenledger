-- 0001_init.sql (PostgreSQL) — catalogue and ledger.

CREATE TABLE IF NOT EXISTS products (
    id                  TEXT    PRIMARY KEY,
    shop_id             TEXT    NOT NULL,
    name                TEXT    NOT NULL,
    sku                 TEXT    NOT NULL,
    aliases             TEXT    NOT NULL DEFAULT '[]',
    price_amount        BIGINT  NOT NULL,
    price_currency      TEXT    NOT NULL,
    quantity            INTEGER NOT NULL DEFAULT 0,
    base_unit           TEXT    NOT NULL,
    packaging           TEXT    NOT NULL DEFAULT '[]',
    low_stock_threshold INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL,
    UNIQUE (shop_id, sku),
    UNIQUE (shop_id, id)
);

CREATE INDEX IF NOT EXISTS idx_products_shop ON products (shop_id);

CREATE TABLE IF NOT EXISTS transactions (
    id                  TEXT    PRIMARY KEY,
    shop_id             TEXT    NOT NULL,
    type                TEXT    NOT NULL,
    product_id          TEXT,
    product_name        TEXT    NOT NULL,
    quantity            INTEGER NOT NULL,
    unit                TEXT    NOT NULL,
    amount_amount       BIGINT  NOT NULL,
    amount_currency     TEXT    NOT NULL,
    confidence_overall  DOUBLE PRECISION NOT NULL DEFAULT 0,
    confidence_product  DOUBLE PRECISION,
    confidence_quantity DOUBLE PRECISION,
    confidence_amount   DOUBLE PRECISION,
    status              TEXT    NOT NULL,
    occurred_at         TEXT    NOT NULL,
    created_at          TEXT    NOT NULL,
    transcript          TEXT    NOT NULL DEFAULT '',
    FOREIGN KEY (shop_id, product_id) REFERENCES products (shop_id, id)
);

CREATE INDEX IF NOT EXISTS idx_transactions_shop_date ON transactions (shop_id, occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_status ON transactions (shop_id, status);
CREATE INDEX IF NOT EXISTS idx_transactions_product ON transactions (shop_id, product_id);

