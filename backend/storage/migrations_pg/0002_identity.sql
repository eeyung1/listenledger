-- 0002_identity.sql (PostgreSQL) — authentication, shops, sessions, rate limits.

CREATE TABLE IF NOT EXISTS owners (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    recovery_hash TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS shops (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL UNIQUE REFERENCES owners(id),
    name TEXT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'NGN',
    timezone TEXT NOT NULL DEFAULT 'Africa/Lagos',
    confidence_threshold DOUBLE PRECISION NOT NULL DEFAULT 0.75,
    sale_phrase TEXT NOT NULL DEFAULT 'Thank you',
    restock_phrase TEXT NOT NULL DEFAULT 'Stock received'
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id),
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_owner ON sessions(owner_id);

CREATE TABLE IF NOT EXISTS auth_attempts (
    key TEXT PRIMARY KEY,
    attempts INTEGER NOT NULL,
    expires_at TEXT NOT NULL
);

