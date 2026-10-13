CREATE TABLE ledger_requests (
 shop_id TEXT NOT NULL,
 request_key TEXT NOT NULL,
 payload_hash TEXT NOT NULL,
 transaction_id TEXT NOT NULL,
 PRIMARY KEY (shop_id, request_key),
 FOREIGN KEY (shop_id, transaction_id) REFERENCES transactions(shop_id, id)
);
-- SQLite requires a unique target for the composite request foreign key.
CREATE UNIQUE INDEX idx_transactions_shop_id ON transactions(shop_id,id);
CREATE TABLE stock_movements (
 transaction_id TEXT PRIMARY KEY,
 shop_id TEXT NOT NULL,
 product_id TEXT NOT NULL,
 delta INTEGER NOT NULL,
 created_at TEXT NOT NULL,
 FOREIGN KEY (shop_id,transaction_id) REFERENCES transactions(shop_id,id),
 FOREIGN KEY (shop_id,product_id) REFERENCES products(shop_id,id)
);
