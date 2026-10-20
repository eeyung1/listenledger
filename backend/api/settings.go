package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

type settingsResponse struct {
	Shop    storage.Shop     `json:"shop"`
	Devices []storage.Device `json:"devices"`
}

type updateSettingsRequest struct {
	Name                *string  `json:"name"`
	Currency            *string  `json:"currency"`
	Timezone            *string  `json:"timezone"`
	ConfidenceThreshold *float64 `json:"confidence_threshold"`
	SalePhrase          *string  `json:"sale_phrase"`
	RestockPhrase       *string  `json:"restock_phrase"`
	RecognitionLanguage *string  `json:"recognition_language"`
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getSettings(w, r)
	case http.MethodPatch:
		s.updateSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, PATCH")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	shop, err := s.store.GetShop(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "get shop settings", err)
		return
	}

	devices, err := s.store.ListDevices(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "list shop devices", err)
		return
	}

	writeJSON(w, http.StatusOK, settingsResponse{
		Shop:    shop,
		Devices: devices,
	})
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req updateSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	updated, err := s.store.UpdateShopSettings(r.Context(), shopID, storage.UpdateShopSettingsParams{
		Name:                req.Name,
		Currency:            req.Currency,
		Timezone:            req.Timezone,
		ConfidenceThreshold: req.ConfidenceThreshold,
		SalePhrase:          req.SalePhrase,
		RestockPhrase:       req.RestockPhrase,
		RecognitionLanguage: req.RecognitionLanguage,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "shop not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.publish(w, r, shopID, ws.EventSettingsUpdated, updated)
	writeJSON(w, http.StatusOK, updated)
}

type purgeTranscriptsRequest struct {
	RetentionDays *int    `json:"retention_days"`
	OlderThan     *string `json:"older_than"`
}

type purgeTranscriptsResponse struct {
	PurgedCount int64  `json:"purged_count"`
	OlderThan   string `json:"older_than"`
}

func (s *Server) handlePurgeTranscripts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req purgeTranscriptsRequest
	if r.Body != nil && r.ContentLength > 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}

	// Operators run the command documented in RUNBOOK.md, which passes the
	// window as query parameters. Accept those as well as the JSON body so the
	// request actually controls the cutoff instead of silently falling back to
	// the 30-day default (MVP_READINESS_AUDIT.md finding 6).
	query := r.URL.Query()
	if req.OlderThan == nil || *req.OlderThan == "" {
		if raw := strings.TrimSpace(query.Get("older_than")); raw != "" {
			req.OlderThan = &raw
		}
	}
	if req.RetentionDays == nil {
		if raw := strings.TrimSpace(query.Get("retention_days")); raw != "" {
			days, err := strconv.Atoi(raw)
			if err != nil || days <= 0 {
				writeError(w, http.StatusBadRequest, "invalid retention_days (expected a positive integer)")
				return
			}
			req.RetentionDays = &days
		}
	}

	var cutoff time.Time
	if req.OlderThan != nil && *req.OlderThan != "" {
		parsed, err := time.Parse(time.RFC3339, *req.OlderThan)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid older_than timestamp (expected RFC3339)")
			return
		}
		cutoff = parsed
	} else if req.RetentionDays != nil && *req.RetentionDays > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -*req.RetentionDays)
	} else {
		// Default to configured retention policy (7 days for pilot, or active retention worker window)
		days := DefaultRetentionDays
		if s.retentionWorker != nil {
			days = s.retentionWorker.RetentionDays()
		}
		cutoff = time.Now().UTC().AddDate(0, 0, -days)
	}

	purged, err := s.store.PurgeTranscripts(r.Context(), shopID, cutoff)
	if err != nil {
		s.fail(w, r, "purge transcripts", err)
		return
	}

	writeJSON(w, http.StatusOK, purgeTranscriptsResponse{
		PurgedCount: purged,
		OlderThan:   cutoff.Format(time.RFC3339),
	})
}

type targetGatesResponse struct {
	Gates    models.TargetGates `json:"gates"`
	IsAgreed bool               `json:"is_agreed"`
}

func (s *Server) handleTargetGates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getTargetGates(w, r)
	case http.MethodPut:
		s.updateTargetGates(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) getTargetGates(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	gates, isAgreed, err := s.store.GetTargetGates(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "get target gates", err)
		return
	}

	writeJSON(w, http.StatusOK, targetGatesResponse{
		Gates:    gates,
		IsAgreed: isAgreed,
	})
}

func (s *Server) updateTargetGates(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req models.TargetGates
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	agreedBy := strings.TrimSpace(req.AgreedBy)
	if agreedBy == "" {
		shop, err := s.store.GetShop(r.Context(), shopID)
		if err == nil && shop.OwnerID != "" {
			agreedBy = "Owner (" + shop.OwnerID + ")"
		} else {
			agreedBy = "Shop Owner"
		}
	}

	updated, err := s.store.SetTargetGates(r.Context(), shopID, req, agreedBy)
	if err != nil {
		s.fail(w, r, "update target gates", err)
		return
	}

	s.publish(w, r, shopID, ws.EventTargetGatesUpdated, updated)
	writeJSON(w, http.StatusOK, targetGatesResponse{
		Gates:    updated,
		IsAgreed: true,
	})
}
