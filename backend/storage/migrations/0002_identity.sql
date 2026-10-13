CREATE TABLE owners (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    recovery_hash TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE shops (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL UNIQUE REFERENCES owners(id),
    name TEXT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'NGN',
    timezone TEXT NOT NULL DEFAULT 'Africa/Lagos',
    confidence_threshold REAL NOT NULL DEFAULT 0.75,
    sale_phrase TEXT NOT NULL DEFAULT 'Thank you',
    restock_phrase TEXT NOT NULL DEFAULT 'Stock received'
);
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES owners(id),
    expires_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_owner ON sessions(owner_id);
CREATE TABLE auth_attempts (
    key TEXT PRIMARY KEY,
    attempts INTEGER NOT NULL,
    expires_at TEXT NOT NULL
);
