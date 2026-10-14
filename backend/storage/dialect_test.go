package storage

import "testing"

func TestRebindQuery(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		query   string
		want    string
	}{
		{
			name:    "SQLite leaves ? placeholders intact",
			dialect: DialectSQLite,
			query:   "SELECT id, name FROM products WHERE shop_id = ? AND sku = ?",
			want:    "SELECT id, name FROM products WHERE shop_id = ? AND sku = ?",
		},
		{
			name:    "Postgres converts ? to $1, $2, ...",
			dialect: DialectPostgres,
			query:   "SELECT id, name FROM products WHERE shop_id = ? AND sku = ?",
			want:    "SELECT id, name FROM products WHERE shop_id = $1 AND sku = $2",
		},
		{
			name:    "Postgres ignores ? inside single-quoted strings",
			dialect: DialectPostgres,
			query:   "SELECT * FROM products WHERE name = 'Is this a question?' AND shop_id = ? AND quantity >= ?",
			want:    "SELECT * FROM products WHERE name = 'Is this a question?' AND shop_id = $1 AND quantity >= $2",
		},
		{
			name:    "Postgres handles multiple placeholders",
			dialect: DialectPostgres,
			query:   "INSERT INTO products (id, shop_id, name, sku, price_amount) VALUES (?, ?, ?, ?, ?)",
			want:    "INSERT INTO products (id, shop_id, name, sku, price_amount) VALUES ($1, $2, $3, $4, $5)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rebindQuery(tc.dialect, tc.query)
			if got != tc.want {
				t.Fatalf("rebindQuery mismatch:\n got:  %s\n want: %s", got, tc.want)
			}
		})
	}
}
