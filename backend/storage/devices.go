package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Device represents a counter capture device paired to a shop.
type Device struct {
	ID        string     `json:"id"`
	ShopID    string     `json:"shop_id"`
	Name      string     `json:"name"`
	TokenHash string     `json:"-"`
	PairedAt  time.Time  `json:"paired_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// PairDevice registers a new edge capture device.
func (s *Store) PairDevice(ctx context.Context, d Device) error {
	if d.ID == "" || d.ShopID == "" || d.Name == "" || d.TokenHash == "" {
		return errors.New("storage: invalid device parameters")
	}

	query := `INSERT INTO devices (id, shop_id, name, token_hash, paired_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, NULL)`
	_, err := s.db.ExecContext(ctx, query, d.ID, d.ShopID, d.Name, d.TokenHash, formatTime(d.PairedAt))
	if err != nil {
		return fmt.Errorf("storage: pair device: %w", err)
	}
	return nil
}

// GetDeviceByTokenHash looks up an active (unrevoked) device by the SHA-256 hash of its token.
func (s *Store) GetDeviceByTokenHash(ctx context.Context, tokenHash string) (Device, error) {
	query := `SELECT id, shop_id, name, token_hash, paired_at, revoked_at
		FROM devices
		WHERE token_hash = ?`

	var d Device
	var pairedAtStr string
	var revokedAtStr sql.NullString

	err := s.db.QueryRowContext(ctx, query, tokenHash).Scan(
		&d.ID, &d.ShopID, &d.Name, &d.TokenHash, &pairedAtStr, &revokedAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Device{}, ErrNotFound
		}
		return Device{}, fmt.Errorf("storage: query device: %w", err)
	}

	pairedAt, err := parseTime(pairedAtStr)
	if err != nil {
		return Device{}, fmt.Errorf("storage: parse paired_at: %w", err)
	}
	d.PairedAt = pairedAt

	if revokedAtStr.Valid {
		revokedAt, err := parseTime(revokedAtStr.String)
		if err != nil {
			return Device{}, fmt.Errorf("storage: parse revoked_at: %w", err)
		}
		d.RevokedAt = &revokedAt
	}

	return d, nil
}

// RevokeDevice revokes a paired device.
func (s *Store) RevokeDevice(ctx context.Context, shopID, deviceID string, revokedAt time.Time) error {
	query := `UPDATE devices
		SET revoked_at = ?
		WHERE shop_id = ? AND id = ? AND revoked_at IS NULL`

	res, err := s.db.ExecContext(ctx, query, formatTime(revokedAt), shopID, deviceID)
	if err != nil {
		return fmt.Errorf("storage: revoke device: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDevices returns all paired devices for a shop.
func (s *Store) ListDevices(ctx context.Context, shopID string) ([]Device, error) {
	query := `SELECT id, shop_id, name, token_hash, paired_at, revoked_at
		FROM devices
		WHERE shop_id = ?
		ORDER BY paired_at ASC`

	rows, err := s.db.QueryContext(ctx, query, shopID)
	if err != nil {
		return nil, fmt.Errorf("storage: list devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var d Device
		var pairedAtStr string
		var revokedAtStr sql.NullString

		if err := rows.Scan(&d.ID, &d.ShopID, &d.Name, &d.TokenHash, &pairedAtStr, &revokedAtStr); err != nil {
			return nil, fmt.Errorf("storage: scan device: %w", err)
		}
		pairedAt, err := parseTime(pairedAtStr)
		if err != nil {
			return nil, err
		}
		d.PairedAt = pairedAt

		if revokedAtStr.Valid {
			revokedAt, err := parseTime(revokedAtStr.String)
			if err != nil {
				return nil, err
			}
			d.RevokedAt = &revokedAt
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate devices: %w", err)
	}
	return devices, nil
}
