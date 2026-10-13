package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/storage"
)

type pairDeviceRequest struct {
	Name string `json:"name"`
}

type pairDeviceResponse struct {
	ID       string    `json:"id"`
	ShopID   string    `json:"shop_id"`
	Name     string    `json:"name"`
	Token    string    `json:"token"` // raw token shown once
	PairedAt time.Time `json:"paired_at"`
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listDevices(w, r)
	case http.MethodPost:
		s.pairDevice(w, r)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) handleDeviceDetail(w http.ResponseWriter, r *http.Request) {
	// e.g. /api/devices/{id}/revoke
	path := strings.TrimPrefix(r.URL.Path, "/api/devices/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 2 && r.Method == http.MethodPost {
		switch parts[1] {
		case "revoke":
			s.revokeDevice(w, r, parts[0])
			return
		case "rotate":
			s.rotateDevice(w, r, parts[0])
			return
		}
	}
	writeError(w, http.StatusNotFound, "device action not found")
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	devices, err := s.store.ListDevices(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "list devices", err)
		return
	}
	if devices == nil {
		devices = []storage.Device{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"shop_id": shopID,
		"devices": devices,
	})
}

func (s *Server) pairDevice(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req pairDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "device name is required")
		return
	}

	id, err := newID("dev")
	if err != nil {
		s.fail(w, r, "generate device id", err)
		return
	}

	var rawTokenBytes [24]byte
	if _, err := rand.Read(rawTokenBytes[:]); err != nil {
		s.fail(w, r, "generate device token", err)
		return
	}
	rawToken := "dev_tok_" + hex.EncodeToString(rawTokenBytes[:])
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	now := time.Now().UTC()
	device := storage.Device{
		ID:        id,
		ShopID:    shopID,
		Name:      name,
		TokenHash: tokenHash,
		PairedAt:  now,
	}

	if err := s.store.PairDevice(r.Context(), device); err != nil {
		s.fail(w, r, "store paired device", err)
		return
	}

	writeJSON(w, http.StatusCreated, pairDeviceResponse{
		ID:       id,
		ShopID:   shopID,
		Name:     name,
		Token:    rawToken,
		PairedAt: now,
	})
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request, deviceID string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	if err := s.store.RevokeDevice(r.Context(), shopID, deviceID, time.Now().UTC()); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found or already revoked")
			return
		}
		s.fail(w, r, "revoke device", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "revoked",
		"id":     deviceID,
	})
}

type rotateDeviceResponse struct {
	ID        string    `json:"id"`
	ShopID    string    `json:"shop_id"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	RotatedAt time.Time `json:"rotated_at"`
}

func (s *Server) rotateDevice(w http.ResponseWriter, r *http.Request, deviceID string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var rawTokenBytes [24]byte
	if _, err := rand.Read(rawTokenBytes[:]); err != nil {
		s.fail(w, r, "generate device token", err)
		return
	}
	rawToken := "dev_tok_" + hex.EncodeToString(rawTokenBytes[:])
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])
	now := time.Now().UTC()

	dev, err := s.store.RotateDeviceToken(r.Context(), shopID, deviceID, tokenHash, now)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		s.fail(w, r, "rotate device token", err)
		return
	}

	writeJSON(w, http.StatusOK, rotateDeviceResponse{
		ID:        dev.ID,
		ShopID:    dev.ShopID,
		Name:      dev.Name,
		Token:     rawToken,
		RotatedAt: now,
	})
}
