package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// Shop contains configuration and threshold settings for a merchant.
type Shop struct {
	ID                  string  `json:"id"`
	OwnerID             string  `json:"owner_id"`
	Name                string  `json:"name"`
	Currency            string  `json:"currency"`
	Timezone            string  `json:"timezone"`
	ConfidenceThreshold float64 `json:"confidence_threshold"`
	SalePhrase          string  `json:"sale_phrase"`
	RestockPhrase       string  `json:"restock_phrase"`
}

// GetShop retrieves a shop by ID, including its thresholds and boundary phrases.
func (s *Store) GetShop(ctx context.Context, shopID string) (Shop, error) {
	query := `SELECT id, owner_id, name, currency, timezone, confidence_threshold, sale_phrase, restock_phrase
		FROM shops WHERE id = ?`
	var sh Shop
	err := s.db.QueryRowContext(ctx, query, shopID).Scan(
		&sh.ID, &sh.OwnerID, &sh.Name, &sh.Currency, &sh.Timezone,
		&sh.ConfidenceThreshold, &sh.SalePhrase, &sh.RestockPhrase,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Shop{}, ErrNotFound
		}
		return Shop{}, fmt.Errorf("storage: get shop %s: %w", shopID, err)
	}
	return sh, nil
}

// UpdateShopSettingsParams specifies configurable shop properties.
type UpdateShopSettingsParams struct {
	Name                *string  `json:"name"`
	Currency            *string  `json:"currency"`
	Timezone            *string  `json:"timezone"`
	ConfidenceThreshold *float64 `json:"confidence_threshold"`
	SalePhrase          *string  `json:"sale_phrase"`
	RestockPhrase       *string  `json:"restock_phrase"`
}

// UpdateShopSettings updates the configuration for an existing shop.
func (s *Store) UpdateShopSettings(ctx context.Context, shopID string, params UpdateShopSettingsParams) (Shop, error) {
	if shopID == "" {
		return Shop{}, errors.New("storage: UpdateShopSettings requires a shop_id")
	}

	current, err := s.GetShop(ctx, shopID)
	if err != nil {
		return Shop{}, err
	}

	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return Shop{}, errors.New("storage: shop name cannot be empty")
		}
		current.Name = name
	}
	if params.Currency != nil {
		curr := strings.ToUpper(strings.TrimSpace(*params.Currency))
		if len(curr) != 3 {
			return Shop{}, errors.New("storage: currency must be a 3-letter code")
		}
		current.Currency = curr
	}
	if params.Timezone != nil {
		tz := strings.TrimSpace(*params.Timezone)
		if _, err := time.LoadLocation(tz); err != nil {
			return Shop{}, fmt.Errorf("storage: invalid timezone %q: %w", tz, err)
		}
		current.Timezone = tz
	}
	if params.ConfidenceThreshold != nil {
		thresh := *params.ConfidenceThreshold
		if thresh < 0.50 || thresh > 1.00 {
			return Shop{}, errors.New("storage: confidence threshold must be between 0.50 and 1.00")
		}
		current.ConfidenceThreshold = thresh
	}
	if params.SalePhrase != nil {
		phrase := strings.TrimSpace(*params.SalePhrase)
		if phrase == "" {
			return Shop{}, errors.New("storage: sale phrase cannot be empty")
		}
		current.SalePhrase = phrase
	}
	if params.RestockPhrase != nil {
		phrase := strings.TrimSpace(*params.RestockPhrase)
		if phrase == "" {
			return Shop{}, errors.New("storage: restock phrase cannot be empty")
		}
		current.RestockPhrase = phrase
	}
	if strings.EqualFold(current.SalePhrase, current.RestockPhrase) {
		return Shop{}, errors.New("storage: sale phrase and restock phrase must be distinct")
	}

	const query = `UPDATE shops SET name = ?, currency = ?, timezone = ?,
		confidence_threshold = ?, sale_phrase = ?, restock_phrase = ?
		WHERE id = ?`
	result, err := s.db.ExecContext(ctx, query,
		current.Name, current.Currency, current.Timezone,
		current.ConfidenceThreshold, current.SalePhrase, current.RestockPhrase,
		shopID,
	)
	if err != nil {
		return Shop{}, fmt.Errorf("storage: update shop %s: %w", shopID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Shop{}, fmt.Errorf("storage: update shop %s rows affected: %w", shopID, err)
	}
	if affected == 0 {
		return Shop{}, ErrNotFound
	}
	return current, nil
}
