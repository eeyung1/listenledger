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
	ReceiptID      string                 `json:"receipt_id,omitempty"`
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

	// P0 pilot policy: discard snippets even from stale clients or offline retries.
	// Retention may only return after P6 opt-in, access and deletion controls exist.
	req.AudioData = ""

	// Park spoken credit attribution without silently treating credit as cash.
	spokenCustomer, spokenCredit, _ := validate.ExtractCustomer(req.Transcript)
	if req.IsCreditSale || req.CustomerID != "" || req.CustomerName != "" || spokenCredit || spokenCustomer != "" {
		writeError(w, http.StatusUnprocessableEntity, "Customer credit capture is parked for this pilot. Keep this sale in your independent credit records; no ledger entry was created.")
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

	// Canonical device identity enforcement:
	// A device cannot masquerade as another device ID.
	if deviceID != "" {
		if req.DeviceID != "" && req.DeviceID != deviceID {
			writeError(w, http.StatusBadRequest, "conflicting device identifier: does not match authenticated device token")
			return
		}
		req.DeviceID = deviceID
	} else if req.DeviceID != "" {
		// If authenticated via user session, verify that the submitted device ID is paired and active
		dev, err := s.store.GetDeviceByID(r.Context(), shopID, req.DeviceID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "unknown or invalid device identifier")
				return
			}
			s.fail(w, r, "lookup submitted device", err)
			return
		}
		if dev.RevokedAt != nil {
			writeError(w, http.StatusBadRequest, "submitted device identifier is revoked")
			return
		}
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

	customers, _, err := s.store.ListCustomers(r.Context(), storage.CustomerQuery{
		ShopID: shopID,
		Limit:  200,
	})
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		s.fail(w, r, "load customers for capture", err)
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
		customers,
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

	var (
		txns         []models.Transaction
		keys         []string
		fingerprints []string
	)

	for i, line := range validationRes.Lines {
		line.Transaction.ReceiptID = req.CaptureID

		key := fmt.Sprintf("%s_%d", idempotencyBase, i)
		if len(key) > 128 {
			key = key[:128]
		}

		rawName := strings.TrimSpace(line.RawAnchor.ProductName)
		if rawName == "" {
			rawName = strings.TrimSpace(line.Transaction.ProductName)
		}
		rawQty := line.RawAnchor.Quantity
		if rawQty <= 0 {
			rawQty = line.Transaction.Quantity
		}
		rawUnit := string(line.RawAnchor.Unit)
		if rawUnit == "" {
			rawUnit = string(line.Transaction.Unit)
		}
		rawAmount := line.Transaction.Amount.Amount
		rawCurrency := line.Transaction.Amount.Currency
		if line.RawAnchor.Amount != nil {
			rawAmount = line.RawAnchor.Amount.Amount
			if line.RawAnchor.Amount.Currency != "" {
				rawCurrency = line.RawAnchor.Amount.Currency
			}
		}

		anchorFP := struct {
			CaptureID   string `json:"capture_id"`
			Index       int    `json:"index"`
			Type        string `json:"type"`
			ProductName string `json:"product_name"`
			Quantity    int    `json:"quantity"`
			Unit        string `json:"unit"`
			Amount      int64  `json:"amount"`
			Currency    string `json:"currency"`
		}{
			CaptureID:   req.CaptureID,
			Index:       i,
			Type:        string(validationRes.Type),
			ProductName: rawName,
			Quantity:    rawQty,
			Unit:        rawUnit,
			Amount:      rawAmount,
			Currency:    rawCurrency,
		}

		canonical, _ := json.Marshal(anchorFP)
		sum := sha256.Sum256(canonical)
		hashStr := hex.EncodeToString(sum[:])

		txns = append(txns, line.Transaction)
		keys = append(keys, key)
		fingerprints = append(fingerprints, hashStr)
	}

	results, err := s.store.RecordReceiptMovements(r.Context(), txns, keys, fingerprints)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrInsufficientStock):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, storage.ErrNotFound), errors.Is(err, models.ErrPackaging), errors.Is(err, storage.ErrCurrency), errors.Is(err, storage.ErrInvalidMovement):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.fail(w, r, "record capture receipt movements", err)
		}
		return
	}

	var storedTransactions []models.Transaction
	allReplayed := len(results) > 0

	for _, result := range results {
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
			} else if stored.Status == models.StatusCancelled {
				eventType = ws.EventTransactionCancelled
			}
			s.publish(w, r, shopID, eventType, stored)
		}
	}

	// Spoken Customer Credit Book Integration (Blueprint §18)
	if !allReplayed && validationRes.IsCreditSale && validationRes.CustomerID != "" {
		var totalCreditAmount int64
		for _, result := range results {
			if !result.Replayed && result.Transaction.Status == models.StatusConfirmed &&
				result.Transaction.Type == models.TypeSale &&
				result.Transaction.CustomerID == validationRes.CustomerID {
				totalCreditAmount += result.Transaction.Amount.Amount
			}
		}

		if totalCreditAmount > 0 {
			debtID := fmt.Sprintf("dbt_cap_%s", req.CaptureID)
			debtDesc := fmt.Sprintf("Voice credit purchase (%s)", req.CaptureID)
			debtOccurredAt := time.Now().UTC()
			if len(storedTransactions) > 0 && !storedTransactions[0].OccurredAt.IsZero() {
				debtOccurredAt = storedTransactions[0].OccurredAt
			}
			recordedDebt, err := s.store.RecordCustomerDebt(r.Context(), models.CustomerDebt{
				ID:          debtID,
				ShopID:      shopID,
				CustomerID:  validationRes.CustomerID,
				Type:        models.DebtTypeCreditSale,
				Amount:      models.Money{Amount: totalCreditAmount, Currency: currency},
				Description: debtDesc,
				ReceiptRef:  req.CaptureID,
				OccurredAt:  debtOccurredAt,
				CreatedBy:   "voice_intake",
			})
			if err == nil {
				updatedCust, err := s.store.GetCustomerByID(r.Context(), shopID, validationRes.CustomerID)
				if err == nil {
					s.publish(w, r, shopID, ws.EventCustomerDebtRecorded, map[string]any{
						"debt":     recordedDebt,
						"customer": updatedCust,
					})
				}
			}
		}
	}

	statusCode := http.StatusCreated
	if allReplayed {
		statusCode = http.StatusOK
	}

	writeJSON(w, statusCode, captureResponse{
		CaptureID:      req.CaptureID,
		ReceiptID:      req.CaptureID,
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

