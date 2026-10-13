package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/eeyung1/listenledger-backend/storage"
)

// handleSummary serves GET /api/summary — server-authoritative daily overview metrics.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	// Fetch shop timezone and currency configuration
	shop, err := s.store.GetShop(r.Context(), shopID)
	timezone := "Africa/Lagos"
	if err == nil && shop.Timezone != "" {
		timezone = shop.Timezone
	} else if err != nil && !errors.Is(err, storage.ErrNotFound) {
		s.fail(w, r, "get shop for summary", err)
		return
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}

	// Calculate today's trading day start and end in shop's local timezone
	nowLocal := time.Now().In(loc)
	startLocal := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	startUTC := startLocal.UTC()
	endUTC := startLocal.AddDate(0, 0, 1).UTC()

	summary, err := s.store.GetDailySummary(r.Context(), shopID, startUTC, endUTC)
	if err != nil {
		s.fail(w, r, "compute daily summary", err)
		return
	}
	summary.LastSeq = s.lastSeq(shopID)

	writeJSON(w, http.StatusOK, summary)
}
