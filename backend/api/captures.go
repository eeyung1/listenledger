package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/validate"
	"github.com/eeyung1/listenledger-backend/ws"
)

type captureResponse struct {
	CaptureID      string                 `json:"capture_id"`
	ShopID         string                 `json:"shop_id"`
	Type           models.TransactionType `json:"type"`
	Transactions   []models.Transaction   `json:"transactions"`
	RequiresReview bool                   `json:"requires_review"`
}

// handleCaptures handles the edge intake endpoint POST /api/captures.
func (s *Server) handleCaptures(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	// 1. Authenticate device or session
	shopID, deviceID, ok := s.authenticateCaptureCaller(w, r)
	if !ok {
		return
	}

	var req validate.CaptureRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.CaptureID == "" {
		cid, err := newID("cap")
		if err != nil {
			s.fail(w, r, "generate capture id", err)
			return
		}
		req.CaptureID = cid
	}
	if deviceID != "" && req.DeviceID == "" {
		req.DeviceID = deviceID
	}

	// 2. Fetch shop settings and catalogue
	shop, err := s.store.GetShop(r.Context(), shopID)
	currency := "NGN"
	threshold := 0.75
	salePhrase := "Thank you"
	restockPhrase := "Stock received"

	if err == nil {
		if shop.Currency != "" {
			currency = shop.Currency
		}
		if shop.ConfidenceThreshold > 0 {
			threshold = shop.ConfidenceThreshold
		}
		if shop.SalePhrase != "" {
			salePhrase = shop.SalePhrase
		}
		if shop.RestockPhrase != "" {
			restockPhrase = shop.RestockPhrase
		}
	} else if !errors.Is(err, storage.ErrNotFound) {
		s.fail(w, r, "get shop for capture", err)
		return
	}

	catalogue, err := s.store.ListProducts(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "load catalogue for capture", err)
		return
	}

	// 3. Run validation engine
	validationRes, err := s.validateEngine.Validate(
		r.Context(),
		req,
		shopID,
		currency,
		threshold,
		salePhrase,
		restockPhrase,
		catalogue,
	)
	if err != nil {
		s.fail(w, r, "validate capture", err)
		return
	}

	// 4. Record movements atomically
	idempotencyBase := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyBase == "" {
		idempotencyBase = req.CaptureID
	}

	var storedTransactions []models.Transaction
	allReplayed := len(validationRes.Lines) > 0

	for i, line := range validationRes.Lines {
		key := fmt.Sprintf("%s_%d", idempotencyBase, i)
		if len(key) > 128 {
			key = key[:128]
		}

		hashTxn := line.Transaction
		hashTxn.ID = ""
		hashTxn.CreatedAt = time.Time{}
		canonical, _ := json.Marshal(hashTxn)
		sum := sha256.Sum256(canonical)
		hashStr := hex.EncodeToString(sum[:])

		result, err := s.store.RecordMovement(r.Context(), line.Transaction, key, hashStr)
		if err != nil {
			switch {
			case errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrInsufficientStock):
				writeError(w, http.StatusConflict, err.Error())
			case errors.Is(err, storage.ErrNotFound), errors.Is(err, models.ErrPackaging), errors.Is(err, storage.ErrCurrency), errors.Is(err, storage.ErrInvalidMovement):
				writeError(w, http.StatusBadRequest, err.Error())
			default:
				s.fail(w, r, "record capture movement", err)
			}
			return
		}

		stored := result.Transaction
		storedTransactions = append(storedTransactions, stored)

		if !result.Replayed {
			allReplayed = false
			if result.Product != nil {
				s.publish(w, r, shopID, ws.EventInventoryUpdated, *result.Product)
			}

			eventType := ws.EventTransactionCreated
			if stored.Status == models.StatusReviewRequired {
				eventType = ws.EventTransactionReviewRequired
			}
			s.publish(w, r, shopID, eventType, stored)
		}
	}

	statusCode := http.StatusCreated
	if allReplayed {
		statusCode = http.StatusOK
	}

	writeJSON(w, statusCode, captureResponse{
		CaptureID:      req.CaptureID,
		ShopID:         shopID,
		Type:           validationRes.Type,
		Transactions:   storedTransactions,
		RequiresReview: validationRes.RequiresReview,
	})
}

// authenticateCaptureCaller authenticates the request via Bearer device token,
// X-Device-Token header, or session fallback.
func (s *Server) authenticateCaptureCaller(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	rawToken := ""
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		rawToken = strings.TrimSpace(authHeader[7:])
	}
	if rawToken == "" {
		rawToken = strings.TrimSpace(r.Header.Get("X-Device-Token"))
	}

	if rawToken != "" {
		hash := sha256.Sum256([]byte(rawToken))
		tokenHash := hex.EncodeToString(hash[:])
		device, err := s.store.GetDeviceByTokenHash(r.Context(), tokenHash)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "invalid or unknown device token")
				return "", "", false
			}
			s.fail(w, r, "lookup device token", err)
			return "", "", false
		}
		if device.RevokedAt != nil {
			writeError(w, http.StatusUnauthorized, "device token has been revoked")
			return "", "", false
		}
		return device.ShopID, device.ID, true
	}

	// Fallback to session / shopID resolver
	shopID, ok := s.shopID(w, r)
	return shopID, "", ok
}
