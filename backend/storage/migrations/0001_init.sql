-- 0001_init.sql — catalogue and ledger.
--
-- Conventions used throughout:
--
--   * Money is stored as two columns: an INTEGER count of the currency's minor
--     unit (kobo for NGN) and the ISO 4217 code. Never a REAL — summing float
--     prices across a trading day drifts, and revenue totals are the number the
--     owner actually reads.
--
--   * Timestamps are TEXT in a fixed-width UTC layout (see storage.formatTime).
--     Fixed width matters: RFC3339Nano drops trailing zeros, so ".5Z" sorts
--     before "Z" and a lexicographic ORDER BY silently misorders rows.
--
--   * shop_id is NOT NULL on every table and leads every index. There is no
--     query in this codebase that is not scoped by it.
--
--   * List-valued columns (aliases, packaging) are JSON TEXT. With 20-50
--     products per shop the catalogue is loaded whole and matched in Go, so
--     normalising them into child tables would add joins without enabling any
--     query the pilot needs.

CREATE TABLE IF NOT EXISTS products (
    id                  TEXT    PRIMARY KEY,
    shop_id             TEXT    NOT NULL,
    name                TEXT    NOT NULL,
    sku                 TEXT    NOT NULL,

    -- JSON array of strings: the local names and pronunciations this product
    -- is actually called in this shop, e.g. ["Peak","Big Peak","Peak tin"].
    aliases             TEXT    NOT NULL DEFAULT '[]',

    price_amount        INTEGER NOT NULL,
    price_currency      TEXT    NOT NULL,

    quantity            INTEGER NOT NULL DEFAULT 0,
    base_unit           TEXT    NOT NULL,

    -- JSON array of {"from","to","factor"}, e.g. 1 carton = 12 packs.
    packaging           TEXT    NOT NULL DEFAULT '[]',

    low_stock_threshold INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL,

    -- A SKU is unique within a shop, not globally: two shops legitimately
    -- stock the same code. This also serves as the lookup index for
    -- GetProductBySKU, so no separate index is needed for that.
    UNIQUE (shop_id, sku),

    -- Redundant with the primary key, but required as the target of the
    -- composite foreign key below, which is what stops a transaction from
    -- referencing another shop's product.
    UNIQUE (shop_id, id)
);

-- Serves the catalogue listing, which is the one query that returns every row
-- for a shop.
CREATE INDEX IF NOT EXISTS idx_products_shop ON products (shop_id);


CREATE TABLE IF NOT EXISTS transactions (
    id                  TEXT    PRIMARY KEY,
    shop_id             TEXT    NOT NULL,

    -- 'sale' (stock out) or 'restock' (stock in).
    type                TEXT    NOT NULL,

    -- NULL until the capture resolves against the catalogue. A Review Required
    -- row is still a complete record: product_name holds what was actually
    -- said, which is what the owner adjudicates against.
    product_id          TEXT,
    product_name        TEXT    NOT NULL,

    quantity            INTEGER NOT NULL,
    unit                TEXT    NOT NULL,

    amount_amount       INTEGER NOT NULL,
    amount_currency     TEXT    NOT NULL,

    -- Per-anchor scores (blueprint §11: product 55% / quantity 98% / amount
    -- 97% is Review Required). NULL means "not scored", which is distinct from
    -- a score of zero.
    confidence_overall  REAL    NOT NULL DEFAULT 0,
    confidence_product  REAL,
    confidence_quantity REAL,
    confidence_amount   REAL,

    -- 'confirmed' | 'review_required' | 'rejected' | 'cancelled'.
    status              TEXT    NOT NULL,

    occurred_at         TEXT    NOT NULL,
    created_at          TEXT    NOT NULL,
    transcript          TEXT    NOT NULL DEFAULT '',

    -- Composite, so the referenced product must belong to the same shop.
    -- product_id is NULL for unresolved captures, and a NULL foreign key is
    -- not checked, which is exactly the behaviour wanted here.
    FOREIGN KEY (shop_id, product_id) REFERENCES products (shop_id, id)
);

-- The snapshot query: newest first, scoped to one shop. Covering the sort means
-- the LIMIT does not require a sort of the whole table.
CREATE INDEX IF NOT EXISTS idx_transactions_shop_occurred
    ON transactions (shop_id, occurred_at DESC);

-- The Review Queue and the dashboard's "needs review" count.
CREATE INDEX IF NOT EXISTS idx_transactions_shop_status
    ON transactions (shop_id, status);

-- Product movement history, and the checks that will be needed before a
-- restock or sale is applied to stock.
CREATE INDEX IF NOT EXISTS idx_transactions_shop_product
    ON transactions (shop_id, product_id);
