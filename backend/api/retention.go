package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/eeyung1/listenledger-backend/storage"
)

// DefaultRetentionDays is the pilot's short retention window for raw speech transcripts
// and legacy audio snippets (P6 privacy policy).
const DefaultRetentionDays = 7

// DefaultRetentionInterval is the default period between automated purge sweeps.
const DefaultRetentionInterval = 24 * time.Hour

// RetentionConfig configures the automated scheduled retention worker.
type RetentionConfig struct {
	RetentionDays int
	Interval      time.Duration
	Logger        *slog.Logger
}

// RetentionStatus describes the active retention policy and scheduler health.
type RetentionStatus struct {
	RetentionDays   int     `json:"retention_days"`
	Interval        string  `json:"interval"`
	IntervalSeconds float64 `json:"interval_seconds"`
	AutoPurgeActive bool    `json:"auto_purge_active"`
	LastRunAt       *string `json:"last_run_at,omitempty"`
	LastPurgedCount int64   `json:"last_purged_count"`
	LastError       *string `json:"last_error,omitempty"`
	NextRunAt       *string `json:"next_run_at,omitempty"`
}

// RetentionWorker runs periodic automated purges of transcripts and audio
// data older than the configured retention window across all shops.
type RetentionWorker struct {
	store  *storage.Store
	logger *slog.Logger

	mu              sync.RWMutex
	retentionDays   int
	interval        time.Duration
	running         bool
	autoPurgeActive bool
	lastRunAt       time.Time
	lastPurgedCount int64
	lastError       error
	nextRunAt       time.Time
	stopCh          chan struct{}
}

// NewRetentionWorker initializes a retention worker handle.
func NewRetentionWorker(store *storage.Store, cfg RetentionConfig) *RetentionWorker {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	days := cfg.RetentionDays
	if days <= 0 {
		days = DefaultRetentionDays
	}

	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultRetentionInterval
	}

	return &RetentionWorker{
		store:           store,
		logger:          logger,
		retentionDays:   days,
		interval:        interval,
		autoPurgeActive: cfg.Interval > 0,
		stopCh:          make(chan struct{}),
	}
}

// Start begins periodic execution of the retention purge in the background.
// It stops when ctx is cancelled or Stop() is called.
func (w *RetentionWorker) Start(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopCh = make(chan struct{})
	interval := w.interval
	w.nextRunAt = time.Now().UTC().Add(interval)
	w.mu.Unlock()

	w.logger.Info("retention worker started",
		"retention_days", w.RetentionDays(),
		"interval", interval.String(),
		"next_run_at", w.nextRunAt.Format(time.RFC3339))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			w.logger.Info("retention worker stopped via context cancellation")
			return
		case <-w.stopCh:
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			w.logger.Info("retention worker stopped via explicit stop")
			return
		case now := <-ticker.C:
			w.mu.Lock()
			w.nextRunAt = now.UTC().Add(w.interval)
			w.mu.Unlock()

			if _, err := w.RunOnce(ctx); err != nil {
				w.logger.Error("retention worker run failed", "error", err)
			}
		}
	}
}

// Stop cleanly requests the background worker loop to terminate.
func (w *RetentionWorker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running && w.stopCh != nil {
		close(w.stopCh)
		w.running = false
	}
}

// RunOnce executes a single retention purge cycle immediately.
func (w *RetentionWorker) RunOnce(ctx context.Context) (int64, error) {
	if w.store == nil {
		return 0, errors.New("retention worker: store is nil")
	}

	w.mu.RLock()
	days := w.retentionDays
	w.mu.RUnlock()

	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	purged, err := w.store.PurgeAllTranscripts(ctx, cutoff)

	now := time.Now().UTC()
	w.mu.Lock()
	w.lastRunAt = now
	w.lastPurgedCount = purged
	w.lastError = err
	w.mu.Unlock()

	if err != nil {
		w.logger.Error("automated retention purge failed",
			"retention_days", days,
			"cutoff", cutoff.Format(time.RFC3339),
			"error", err)
		return 0, fmt.Errorf("retention worker run: %w", err)
	}

	w.logger.Info("automated retention purge complete",
		"retention_days", days,
		"cutoff", cutoff.Format(time.RFC3339),
		"purged_count", purged)

	return purged, nil
}

// RetentionDays returns the current retention cutoff in days.
func (w *RetentionWorker) RetentionDays() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.retentionDays
}

// UpdatePolicy updates the retention days window and interval.
func (w *RetentionWorker) UpdatePolicy(days int, interval time.Duration) error {
	if days <= 0 {
		return errors.New("retention_days must be a positive integer")
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.retentionDays = days
	if interval > 0 {
		w.interval = interval
		w.autoPurgeActive = true
		w.nextRunAt = time.Now().UTC().Add(interval)
	} else if interval < 0 {
		w.autoPurgeActive = false
	}

	w.logger.Info("retention policy updated",
		"retention_days", w.retentionDays,
		"interval", w.interval.String(),
		"auto_purge_active", w.autoPurgeActive)
	return nil
}

// Status returns a snapshot of the retention policy and scheduler status.
func (w *RetentionWorker) Status() RetentionStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()

	status := RetentionStatus{
		RetentionDays:   w.retentionDays,
		Interval:        w.interval.String(),
		IntervalSeconds: w.interval.Seconds(),
		AutoPurgeActive: w.autoPurgeActive && w.running,
		LastPurgedCount: w.lastPurgedCount,
	}

	if !w.lastRunAt.IsZero() {
		s := w.lastRunAt.Format(time.RFC3339)
		status.LastRunAt = &s
	}
	if w.lastError != nil {
		s := w.lastError.Error()
		status.LastError = &s
	}
	if !w.nextRunAt.IsZero() && w.running {
		s := w.nextRunAt.Format(time.RFC3339)
		status.NextRunAt = &s
	}

	return status
}

// handleRetentionPolicy handles GET and PUT /api/settings/retention-policy
func (s *Server) handleRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getRetentionPolicy(w, r, shopID)
	case http.MethodPut, http.MethodPost:
		s.updateRetentionPolicy(w, r, shopID)
	default:
		w.Header().Set("Allow", "GET, PUT, POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type retentionPolicyResponse struct {
	ShopID      string          `json:"shop_id"`
	Policy      RetentionStatus `json:"policy"`
	GeneratedAt string          `json:"generated_at"`
}

type updateRetentionPolicyRequest struct {
	RetentionDays *int    `json:"retention_days"`
	Interval      *string `json:"interval"`
}

func (s *Server) getRetentionPolicy(w http.ResponseWriter, r *http.Request, shopID string) {
	var status RetentionStatus
	if s.retentionWorker != nil {
		status = s.retentionWorker.Status()
	} else {
		status = RetentionStatus{
			RetentionDays:   DefaultRetentionDays,
			Interval:        DefaultRetentionInterval.String(),
			IntervalSeconds: DefaultRetentionInterval.Seconds(),
			AutoPurgeActive: false,
		}
	}

	writeJSON(w, http.StatusOK, retentionPolicyResponse{
		ShopID:      shopID,
		Policy:      status,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) updateRetentionPolicy(w http.ResponseWriter, r *http.Request, shopID string) {
	var req updateRetentionPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if s.retentionWorker == nil {
		writeError(w, http.StatusServiceUnavailable, "retention worker is not configured on this server")
		return
	}

	days := s.retentionWorker.RetentionDays()
	if req.RetentionDays != nil {
		if *req.RetentionDays <= 0 {
			writeError(w, http.StatusBadRequest, "retention_days must be a positive integer")
			return
		}
		days = *req.RetentionDays
	}

	interval := time.Duration(0)
	if req.Interval != nil && strings.TrimSpace(*req.Interval) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(*req.Interval))
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid interval (must be a valid duration string, e.g. '24h', '1h')")
			return
		}
		interval = parsed
	}

	if err := s.retentionWorker.UpdatePolicy(days, interval); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.getRetentionPolicy(w, r, shopID)
}

// SetRetentionWorker attaches a RetentionWorker instance to the server.
func (s *Server) SetRetentionWorker(worker *RetentionWorker) {
	s.retentionWorker = worker
}

// RetentionWorker returns the server's attached RetentionWorker instance, if any.
func (s *Server) RetentionWorker() *RetentionWorker {
	return s.retentionWorker
}
