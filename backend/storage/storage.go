// Package storage owns persistence for the ListenLedger orchestrator.
//
// SQLite is the pilot's store: one shop, one counter, a few dozen products and
// a few hundred transactions a day. The schema and queries are written so the
// move to Postgres in the 10-15 shop phase is a driver swap plus the migration
// files, not a rewrite of the access layer.
//
// Two rules hold everywhere in this package:
//
//   - Every query is scoped by shop_id. There is no method that reads across
//     shops, so tenancy cannot be forgotten at a call site.
//   - Money is integer minor units and timestamps are fixed-width UTC strings.
//     See migrations/0001_init.sql for why.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	// PostgreSQL driver via pgx stdlib
	_ "github.com/jackc/pgx/v5/stdlib"

	// CGO SQLite driver. Requires CGO_ENABLED=1 and a C toolchain; see
	// TODO.md for the pure-Go alternative if that becomes a problem for
	// cross-compiling the orchestrator.
	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

//go:embed migrations_pg/*.sql
var migrationPgFS embed.FS

// timeLayout is the on-disk timestamp format: UTC, fixed width, always nine
// fractional digits.
//
// The fixed width is load-bearing. time.RFC3339Nano omits trailing zeros, so
// "10:00:00.5Z" sorts before "10:00:00Z" because '.' precedes 'Z', and any
// lexicographic ORDER BY would misorder rows within the same second.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// ErrNotFound reports that a row does not exist, or does not exist *for this
// shop*. The two are deliberately indistinguishable: a caller scoped to one
// shop has no business learning that an ID exists in another.
var ErrNotFound = errors.New("storage: not found")

// ErrInsufficientStock reports that a stock adjustment was refused because it
// would have driven the quantity negative.
var ErrInsufficientStock = errors.New("storage: adjustment would make stock negative")

// Store is the persistence handle.
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// Dialect returns the active database engine dialect.
func (s *Store) Dialect() Dialect {
	if s == nil || s.dialect == "" {
		return DialectSQLite
	}
	return s.dialect
}

// rebind converts '?' positional placeholders to the active dialect's format ($1, $2, ... for Postgres).
func (s *Store) rebind(query string) string {
	return rebindQuery(s.Dialect(), query)
}

// Open connects to either SQLite or PostgreSQL depending on the DSN format:
// If dsn starts with "postgres://" or "postgresql://", it opens a PostgreSQL connection.
// Otherwise, it opens a SQLite connection at path.
//
// Use ":memory:" for a shared in-memory database in tests.
func Open(dsn string) (*Store, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return OpenPostgres(dsn)
	}
	return OpenSQLite(dsn)
}

// OpenSQLite connects to the SQLite database at path, applies pragmas, and runs any
// outstanding SQLite migrations.
func OpenSQLite(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", buildDSN(path))
	if err != nil {
		return nil, fmt.Errorf("storage: open sqlite %s: %w", path, err)
	}

	// SQLite allows one writer at a time. A small pool with a busy timeout
	// lets concurrent readers proceed while writers queue rather than fail
	// with SQLITE_BUSY. Connections are long-lived: there is no point
	// recycling them, and closing one would drop its per-connection pragmas.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage: ping sqlite %s: %w", path, err)
	}

	store := &Store{db: db, dialect: DialectSQLite}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// OpenPostgres connects to PostgreSQL using the provided connection string (URL),
// configures production-grade connection pooling, and runs any outstanding PostgreSQL migrations.
func OpenPostgres(connStr string) (*Store, error) {
	return openPostgres(connStr, true)
}

// OpenPostgresRuntime never performs DDL. Deployment must apply migrations with
// an administrative connection before starting the server with its runtime role.
func OpenPostgresRuntime(connStr string) (*Store, error) {
	if !strings.HasPrefix(connStr, "postgres://") && !strings.HasPrefix(connStr, "postgresql://") {
		return nil, errors.New("storage: PostgreSQL connection URL required")
	}
	return openPostgres(connStr, false)
}

func openPostgres(connStr string, migrate bool) (*Store, error) {
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("storage: open postgres: %w", err)
	}

	// Production-grade connection pooling per DATABASE_MIGRATION.md §1
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage: ping postgres: %w", err)
	}

	store := &Store{db: db, dialect: DialectPostgres}
	var schemaErr error
	if migrate {
		schemaErr = store.migrate(ctx)
	} else {
		schemaErr = store.verifyMigrations(ctx)
	}
	if schemaErr != nil {
		_ = db.Close()
		return nil, schemaErr
	}
	return store, nil
}

func (s *Store) verifyMigrations(ctx context.Context) error {
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return fmt.Errorf("storage: schema unavailable; apply migrations with an administrator first: %w", err)
	}
	names, err := fs.Glob(migrationPgFS, "migrations_pg/*.sql")
	if err != nil {
		return err
	}
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		if !applied[version] {
			return fmt.Errorf("storage: migration %d missing; apply migrations with an administrator before deploying", version)
		}
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Ping checks whether the underlying database connection is alive and responsive.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("storage: store is closed or not initialized")
	}
	return s.db.PingContext(ctx)
}

// DeleteShopData removes every product and transaction belonging to one shop.
//
// DESTRUCTIVE. It exists for the development seeder, which needs a known
// starting state rather than an ever-growing pile of duplicates, and it is
// reachable only from a -dev process. Deleting a real shop's books is not
// something this codebase should make easy, so nothing else calls it.
//
// Both deletes run in one transaction, ordered children-first: transactions
// carry a composite foreign key into products, so removing the catalogue first
// would be refused mid-way and leave the shop half-deleted.
func (s *Store) DeleteShopData(ctx context.Context, shopID string) error {
	if shopID == "" {
		return errors.New("storage: DeleteShopData requires a shop_id")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin delete for shop %s: %w", shopID, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range []string{"customer_debts", "customers", "expenses", "pilot_discrepancies", "pilot_reconciliations", "shop_cohorts", "shop_target_gates", "review_decisions", "stock_movements", "ledger_requests", "transactions", "products"} {
		query := s.rebind(`DELETE FROM ` + table + ` WHERE shop_id = ?`)
		if _, err := tx.ExecContext(ctx, query, shopID); err != nil {
			return fmt.Errorf("storage: delete %s for shop %s: %w", table, shopID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit delete for shop %s: %w", shopID, err)
	}
	return nil
}

// buildDSN assembles the connection string.
//
// The pragmas are set per connection rather than once at open time, because
// busy_timeout and foreign_keys are connection-scoped in SQLite — setting them
// on one pooled connection would leave the others unprotected. journal_mode is
// persisted in the file, so repeating it is harmless.
func buildDSN(path string) string {
	params := url.Values{}
	// Off by default in SQLite; the composite foreign key in the schema relies
	// on it being on.
	params.Set("_foreign_keys", "on")
	// Write-ahead logging: readers do not block the writer.
	params.Set("_journal_mode", "WAL")
	// Wait rather than error when another connection holds the write lock.
	params.Set("_busy_timeout", "5000")
	// Take the write lock at BEGIN instead of upgrading mid-transaction, which
	// is what turns a read-then-write into a deadlock under concurrency.
	params.Set("_txlock", "immediate")

	if path == ":memory:" {
		// Shared cache, so every connection in the pool sees the same schema
		// instead of each getting its own empty database.
		return "file::memory:?cache=shared&" + params.Encode()
	}
	return "file:" + path + "?" + params.Encode()
}

// querier is the read surface shared by *sql.DB and *sql.Tx, so scanning
// helpers work inside and outside a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// migrate applies every embedded migration that has not run yet.
func (s *Store) migrate(ctx context.Context) error {
	const createMigrations = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`

	if _, err := s.db.ExecContext(ctx, createMigrations); err != nil {
		return fmt.Errorf("storage: create schema_migrations: %w", err)
	}

	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}

	targetFS := migrationFS
	globPattern := "migrations/*.sql"
	if s.Dialect().IsPostgres() {
		targetFS = migrationPgFS
		globPattern = "migrations_pg/*.sql"
	}

	names, err := fs.Glob(targetFS, globPattern)
	if err != nil {
		return fmt.Errorf("storage: list migrations: %w", err)
	}
	// Zero-padded filenames sort correctly as strings.
	sort.Strings(names)

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		if applied[version] {
			continue
		}

		body, err := targetFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("storage: read migration %s: %w", name, err)
		}
		if err := s.applyMigration(ctx, version, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("storage: read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("storage: scan schema_migrations: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate schema_migrations: %w", err)
	}
	return applied, nil
}

// applyMigration runs one migration and records it, in a single transaction.
func (s *Store) applyMigration(ctx context.Context, version int, body string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin migration %d: %w", version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("storage: apply migration %d: %w", version, err)
	}
	recordQuery := s.rebind(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`)
	if _, err := tx.ExecContext(ctx, recordQuery, version, formatTime(time.Now())); err != nil {
		return fmt.Errorf("storage: record migration %d: %w", version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit migration %d: %w", version, err)
	}
	return nil
}

// migrationVersion reads the leading integer from "0001_init.sql".
func migrationVersion(name string) (int, error) {
	base := filepath.Base(name)
	prefix, _, found := strings.Cut(base, "_")
	if !found {
		return 0, fmt.Errorf("storage: migration %q must be named <version>_<name>.sql", base)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("storage: migration %q has a non-numeric version: %w", base, err)
	}
	return version, nil
}

// formatTime renders a timestamp for storage in the fixed-width UTC layout.
func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// parseTime reads a stored timestamp. It accepts any fractional-second width
// so rows written by a different formatter still parse.
func parseTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("storage: parse timestamp %q: %w", raw, err)
	}
	return t.UTC(), nil
}

// nullString maps an empty Go string to SQL NULL.
func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nullFloat maps a nil pointer to SQL NULL. A nil score means "not measured",
// which must not be stored as a real 0.
func nullFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

// Backup creates a clean, atomic snapshot of the database at destPath.
func (s *Store) Backup(ctx context.Context, destPath string) error {
	if s.Dialect().IsPostgres() {
		return errors.New("storage: Backup via VACUUM INTO is SQLite-specific; for PostgreSQL use pg_dump or managed Supabase backups")
	}
	if destPath == "" {
		return errors.New("storage: Backup requires a destination path")
	}
	escapedPath := strings.ReplaceAll(destPath, "'", "''")
	query := fmt.Sprintf("VACUUM INTO '%s'", escapedPath)
	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("storage: vacuum into %s: %w", destPath, err)
	}
	return nil
}

// VerifyIntegrity checks the health and integrity of the database.
func (s *Store) VerifyIntegrity(ctx context.Context) error {
	if s.Dialect().IsPostgres() {
		var result int
		if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
			return fmt.Errorf("storage: postgres integrity check failed: %w", err)
		}
		return nil
	}
	var result string
	if err := s.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("storage: integrity check failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("storage: database integrity corrupted: %s", result)
	}
	return nil
}
