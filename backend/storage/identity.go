package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Identity is the public session state. Secrets never leave the storage/auth layers.
type Identity struct {
	OwnerID  string `json:"owner_id"`
	Email    string `json:"email"`
	ShopID   string `json:"shop_id"`
	ShopName string `json:"shop_name"`
	Currency string `json:"currency"`
	Timezone string `json:"timezone"`
}
type Account struct {
	Identity
	PasswordHash, RecoveryHash string
}

func (s *Store) CreateAccount(ctx context.Context, a Account) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO owners(id,email,password_hash,recovery_hash,created_at) VALUES(?,?,?,?,?)`, a.OwnerID, a.Email, a.PasswordHash, a.RecoveryHash, formatTime(time.Now()))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO shops(id,owner_id,name,currency,timezone) VALUES(?,?,?,?,?)`, a.ShopID, a.OwnerID, a.ShopName, a.Currency, a.Timezone)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) AccountByEmail(ctx context.Context, email string) (Account, error) {
	var a Account
	err := s.db.QueryRowContext(ctx, `SELECT o.id,o.email,s.id,s.name,s.currency,s.timezone,o.password_hash,o.recovery_hash FROM owners o JOIN shops s ON s.owner_id=o.id WHERE o.email=?`, email).Scan(&a.OwnerID, &a.Email, &a.ShopID, &a.ShopName, &a.Currency, &a.Timezone, &a.PasswordHash, &a.RecoveryHash)
	return a, err
}

// CreateSession checks the password version atomically: recovery racing a login cannot resurrect an old session.
func (s *Store) CreateSession(ctx context.Context, hash, owner, password string, expiry time.Time) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash,owner_id,expires_at) SELECT ?,id,? FROM owners WHERE id=? AND password_hash=?`, hash, formatTime(expiry), owner, password)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) SessionIdentity(ctx context.Context, hash string) (Identity, error) {
	var a Identity
	err := s.db.QueryRowContext(ctx, `SELECT o.id,o.email,s.id,s.name,s.currency,s.timezone FROM sessions x JOIN owners o ON o.id=x.owner_id JOIN shops s ON s.owner_id=o.id WHERE x.token_hash=? AND x.expires_at>?`, hash, formatTime(time.Now())).Scan(&a.OwnerID, &a.Email, &a.ShopID, &a.ShopName, &a.Currency, &a.Timezone)
	return a, err
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hash)
	return err
}

// Recovery uses a compare-and-swap, rotates the code and revokes all sessions atomically.
func (s *Store) RecoverAccount(ctx context.Context, owner, oldRecovery, password, recovery string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE owners SET password_hash=?,recovery_hash=? WHERE id=? AND recovery_hash=?`, password, recovery, owner, oldRecovery)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE owner_id=?`, owner); err != nil {
		return err
	}
	return tx.Commit()
}

// AllowAuthAttempt persists a fixed-window limiter so restarts do not reset it.
func (s *Store) AllowAuthAttempt(ctx context.Context, key string, limit int) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := time.Now()
	if _, err = tx.ExecContext(ctx, `DELETE FROM auth_attempts WHERE expires_at<=?`, formatTime(now)); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=?`, formatTime(now)); err != nil {
		return false, err
	}
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT attempts FROM auth_attempts WHERE key=?`, key).Scan(&attempts)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if attempts >= limit {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO auth_attempts(key,attempts,expires_at) VALUES(?,1,?) ON CONFLICT(key) DO UPDATE SET attempts=attempts+1`, key, formatTime(now.Add(15*time.Minute)))
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}
