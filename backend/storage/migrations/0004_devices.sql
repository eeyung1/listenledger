-- 0004_devices.sql — edge device pairing and authorization.
CREATE TABLE IF NOT EXISTS devices (
    id          TEXT PRIMARY KEY,
    shop_id     TEXT NOT NULL REFERENCES shops(id),
    name        TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    paired_at   TEXT NOT NULL,
    revoked_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_devices_shop ON devices(shop_id);

