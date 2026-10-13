package api

import (
	"errors"
	"net/http"

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
