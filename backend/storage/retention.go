package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PurgeAllTranscripts permanently blanks transcript and audio_data fields
// on transactions older than olderThan across all shops.
//
// Ledger rows, movements, line items, and financial amounts are preserved
// untouched. Only the raw speech transcript and legacy audio snippet are purged.
func (s *Store) PurgeAllTranscripts(ctx context.Context, olderThan time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("storage: store not initialized")
	}
	if olderThan.IsZero() {
		return 0, errors.New("storage: PurgeAllTranscripts requires a valid olderThan timestamp")
	}

	const query = `UPDATE transactions
                      SET transcript = '', audio_data = ''
                    WHERE occurred_at < ?
                      AND (transcript != '' OR audio_data != '')`

	res, err := s.db.ExecContext(ctx, s.rebind(query), formatTime(olderThan))
	if err != nil {
		return 0, fmt.Errorf("storage: purge all transcripts: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("storage: purge all transcripts rows affected: %w", err)
	}

	return affected, nil
}

// CountRetentionCandidates reports the number of transactions older than
// olderThan that still retain transcript or audio_data, across all shops.
func (s *Store) CountRetentionCandidates(ctx context.Context, olderThan time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("storage: store not initialized")
	}
	if olderThan.IsZero() {
		return 0, errors.New("storage: CountRetentionCandidates requires a valid olderThan timestamp")
	}

	const query = `SELECT COUNT(*) FROM transactions
                    WHERE occurred_at < ?
                      AND (transcript != '' OR audio_data != '')`

	var count int64
	if err := s.db.QueryRowContext(ctx, s.rebind(query), formatTime(olderThan)).Scan(&count); err != nil {
		return 0, fmt.Errorf("storage: count retention candidates: %w", err)
	}
	return count, nil
}
