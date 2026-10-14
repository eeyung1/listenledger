package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

// GetTargetGates retrieves the agreed target gates for a shop.
// If no explicit targets have been agreed yet, it returns DefaultTargetGates with isAgreed = false.
func (s *Store) GetTargetGates(ctx context.Context, shopID string) (models.TargetGates, bool, error) {
	if shopID == "" {
		return models.TargetGates{}, false, errors.New("storage: GetTargetGates requires a shop_id")
	}

	const query = `SELECT min_full_transaction_accuracy, min_sku_accuracy, min_quantity_accuracy,
		min_unit_accuracy, min_amount_accuracy, max_false_capture_rate, max_missed_transaction_rate,
		max_correction_rate, min_protocol_compliance_rate, max_p95_latency_ms, agreed_at, agreed_by
		FROM shop_target_gates WHERE shop_id = ?`

	var (
		g        models.TargetGates
		agreedAt string
	)

	err := s.db.QueryRowContext(ctx, s.rebind(query), shopID).Scan(
		&g.MinFullTransactionAccuracy,
		&g.MinSKUAccuracy,
		&g.MinQuantityAccuracy,
		&g.MinUnitAccuracy,
		&g.MinAmountAccuracy,
		&g.MaxFalseCaptureRate,
		&g.MaxMissedTransactionRate,
		&g.MaxCorrectionRate,
		&g.MinProtocolComplianceRate,
		&g.MaxP95LatencyMs,
		&agreedAt,
		&g.AgreedBy,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.DefaultTargetGates, false, nil
		}
		return models.TargetGates{}, false, fmt.Errorf("storage: get target gates for %s: %w", shopID, err)
	}

	t, err := parseTime(agreedAt)
	if err == nil {
		g.AgreedAt = &t
	}

	return g, true, nil
}

// SetTargetGates upserts agreed pilot target release gates for a shop.
func (s *Store) SetTargetGates(ctx context.Context, shopID string, gates models.TargetGates, agreedBy string) (models.TargetGates, error) {
	if shopID == "" {
		return models.TargetGates{}, errors.New("storage: SetTargetGates requires a shop_id")
	}
	if err := gates.Validate(); err != nil {
		return models.TargetGates{}, fmt.Errorf("storage: invalid target gates: %w", err)
	}

	agreedBy = strings.TrimSpace(agreedBy)
	if agreedBy == "" {
		agreedBy = "owner"
	}

	now := time.Now().UTC()
	agreedAtStr := formatTime(now)
	gates.AgreedAt = &now
	gates.AgreedBy = agreedBy

	const upsert = `INSERT INTO shop_target_gates (
		shop_id, min_full_transaction_accuracy, min_sku_accuracy, min_quantity_accuracy,
		min_unit_accuracy, min_amount_accuracy, max_false_capture_rate, max_missed_transaction_rate,
		max_correction_rate, min_protocol_compliance_rate, max_p95_latency_ms, agreed_at, agreed_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(shop_id) DO UPDATE SET
		min_full_transaction_accuracy = excluded.min_full_transaction_accuracy,
		min_sku_accuracy = excluded.min_sku_accuracy,
		min_quantity_accuracy = excluded.min_quantity_accuracy,
		min_unit_accuracy = excluded.min_unit_accuracy,
		min_amount_accuracy = excluded.min_amount_accuracy,
		max_false_capture_rate = excluded.max_false_capture_rate,
		max_missed_transaction_rate = excluded.max_missed_transaction_rate,
		max_correction_rate = excluded.max_correction_rate,
		min_protocol_compliance_rate = excluded.min_protocol_compliance_rate,
		max_p95_latency_ms = excluded.max_p95_latency_ms,
		agreed_at = excluded.agreed_at,
		agreed_by = excluded.agreed_by`

	_, err := s.db.ExecContext(ctx, s.rebind(upsert),
		shopID,
		gates.MinFullTransactionAccuracy,
		gates.MinSKUAccuracy,
		gates.MinQuantityAccuracy,
		gates.MinUnitAccuracy,
		gates.MinAmountAccuracy,
		gates.MaxFalseCaptureRate,
		gates.MaxMissedTransactionRate,
		gates.MaxCorrectionRate,
		gates.MinProtocolComplianceRate,
		gates.MaxP95LatencyMs,
		agreedAtStr,
		agreedBy,
	)

	if err != nil {
		return models.TargetGates{}, fmt.Errorf("storage: set target gates for %s: %w", shopID, err)
	}

	return gates, nil
}
