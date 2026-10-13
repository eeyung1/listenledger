package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// StepDetail describes one step in the onboarding progression.
type StepDetail struct {
	Completed   bool   `json:"completed"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Count       int    `json:"count,omitempty"`
}

// OnboardingSteps aggregates the 4 onboarding milestones.
type OnboardingSteps struct {
	Profile     StepDetail `json:"profile"`
	Catalogue   StepDetail `json:"catalogue"`
	Devices     StepDetail `json:"devices"`
	TestCapture StepDetail `json:"test_capture"`
}

// OnboardingStatusResponse reports shop setup progress.
type OnboardingStatusResponse struct {
	ShopID         string          `json:"shop_id"`
	Steps          OnboardingSteps `json:"steps"`
	CompletedSteps int             `json:"completed_steps"`
	TotalSteps     int             `json:"total_steps"`
	IsComplete     bool            `json:"is_complete"`
}

// handleOnboardingStatus serves GET /api/onboarding/status.
func (s *Server) handleOnboardingStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	// 1. Check Shop Profile
	shop, err := s.store.GetShop(r.Context(), shopID)
	profileCompleted := false
	if err == nil {
		profileCompleted = strings.TrimSpace(shop.Name) != "" && strings.TrimSpace(shop.Currency) != ""
	} else if !errors.Is(err, storage.ErrNotFound) {
		s.fail(w, r, "fetch shop for onboarding status", err)
		return
	}

	// 2. Check Catalogue & Opening Stock
	products, err := s.store.ListProducts(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "list catalogue for onboarding status", err)
		return
	}
	catalogueCompleted := len(products) > 0

	// 3. Check Paired Devices
	devices, err := s.store.ListDevices(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "list devices for onboarding status", err)
		return
	}
	activeDeviceCount := 0
	for _, dev := range devices {
		if dev.RevokedAt == nil {
			activeDeviceCount++
		}
	}
	devicesCompleted := activeDeviceCount > 0

	// 4. Check Test Capture / Initial Transaction
	_, totalTxns, err := s.store.QueryTransactions(r.Context(), shopID, storage.TransactionFilter{Limit: 1})
	if err != nil {
		s.fail(w, r, "query transactions for onboarding status", err)
		return
	}
	testCaptureCompleted := totalTxns > 0

	steps := OnboardingSteps{
		Profile: StepDetail{
			Completed:   profileCompleted,
			Title:       "Shop Profile & Settings",
			Description: "Configure shop name, currency, timezone, and closing phrases",
		},
		Catalogue: StepDetail{
			Completed:   catalogueCompleted,
			Title:       "Catalogue & Opening Stock",
			Description: "Add products with opening stock or import inventory via CSV",
			Count:       len(products),
		},
		Devices: StepDetail{
			Completed:   devicesCompleted,
			Title:       "Pair Edge Counter Device",
			Description: "Pair a counter microphone device or generate edge credentials",
			Count:       activeDeviceCount,
		},
		TestCapture: StepDetail{
			Completed:   testCaptureCompleted,
			Title:       "Sound & Capture Test",
			Description: "Verify speech recognition and transaction processing pipeline",
			Count:       totalTxns,
		},
	}

	completedCount := 0
	if profileCompleted {
		completedCount++
	}
	if catalogueCompleted {
		completedCount++
	}
	if devicesCompleted {
		completedCount++
	}
	if testCaptureCompleted {
		completedCount++
	}

	writeJSON(w, http.StatusOK, OnboardingStatusResponse{
		ShopID:         shopID,
		Steps:          steps,
		CompletedSteps: completedCount,
		TotalSteps:     4,
		IsComplete:     completedCount == 4,
	})
}

// testCaptureRequest is the payload of POST /api/onboarding/test-capture.
type testCaptureRequest struct {
	Transcript string                 `json:"transcript"`
	Type       models.TransactionType `json:"type,omitempty"`
}

// testCaptureLineResponse summarizes one recognized line in the test capture.
type testCaptureLineResponse struct {
	ProductName   string                   `json:"product_name"`
	MatchedBy     string                   `json:"matched_by"`
	Quantity      int                      `json:"quantity"`
	Unit          models.Unit              `json:"unit"`
	Amount        models.Money             `json:"amount"`
	Confidence    models.Confidence        `json:"confidence"`
	Status        models.TransactionStatus `json:"status"`
	ReviewReasons []string                 `json:"review_reasons"`
}

// testCaptureResponse is returned by POST /api/onboarding/test-capture.
type testCaptureResponse struct {
	Status           string                    `json:"status"`
	Recognized       bool                      `json:"recognized"`
	Type             models.TransactionType    `json:"type"`
	ClosingPhrase    string                    `json:"closing_phrase"`
	BoundaryDetected bool                      `json:"boundary_detected"`
	Lines            []testCaptureLineResponse `json:"lines"`
	Summary          string                    `json:"summary"`
	TransactionID    string                    `json:"transaction_id,omitempty"`
}

// handleOnboardingTestCapture serves POST /api/onboarding/test-capture.
func (s *Server) handleOnboardingTestCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req testCaptureRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	transcript := strings.TrimSpace(req.Transcript)
	if transcript == "" {
		writeError(w, http.StatusBadRequest, "transcript is required for test capture")
		return
	}

	// 1. Fetch shop settings and catalogue
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
		s.fail(w, r, "get shop for test capture", err)
		return
	}

	catalogue, err := s.store.ListProducts(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "load catalogue for test capture", err)
		return
	}

	// 2. Run validation engine
	capReq := validate.CaptureRequest{
		Type:       req.Type,
		Transcript: transcript,
	}
	validationRes, err := s.validateEngine.Validate(
		r.Context(),
		capReq,
		shopID,
		currency,
		threshold,
		salePhrase,
		restockPhrase,
		catalogue,
	)
	if err != nil {
		s.fail(w, r, "validate test capture", err)
		return
	}

	// 3. Prepare response lines and record test transaction
	var responseLines []testCaptureLineResponse
	recognizedAny := false
	recordedTxnID := ""

	receiptID, err := newID("rcp")
	if err != nil {
		receiptID = fmt.Sprintf("rcp_test_%d", time.Now().UnixNano())
	}

	for _, line := range validationRes.Lines {
		txn := line.Transaction
		txn.ShopID = shopID
		txn.ReceiptID = receiptID
		txn.Status = models.StatusReviewRequired // Test captures always hold for review so stock is never altered unexpectedly

		if txn.ID == "" {
			tid, err := newID("txn")
			if err != nil {
				tid = fmt.Sprintf("txn_test_%d", time.Now().UnixNano())
			}
			txn.ID = tid
		}

		responseLines = append(responseLines, testCaptureLineResponse{
			ProductName:   txn.ProductName,
			MatchedBy:     line.MatchedBy,
			Quantity:      txn.Quantity,
			Unit:          txn.Unit,
			Amount:        txn.Amount,
			Confidence:    txn.Confidence,
			Status:        txn.Status,
			ReviewReasons: line.ReviewReasons,
		})

		if line.MatchedBy != "none" && line.Transaction.ProductID != "" {
			recognizedAny = true
		}

		// Save to ledger as an onboarding test transaction with unique idempotency key
		idemKey := fmt.Sprintf("onboarding_test_%s_%s", shopID, txn.ID)
		sum := sha256.Sum256([]byte(idemKey + transcript))
		result, err := s.store.RecordMovement(context.Background(), txn, idemKey, hex.EncodeToString(sum[:]))
		if err == nil {
			recordedTxnID = result.Transaction.ID
			s.publish(w, r, shopID, ws.EventTransactionReviewRequired, result.Transaction)
		}
	}

	// Formulate human-readable summary
	summary := ""
	if recognizedAny && len(responseLines) > 0 {
		first := responseLines[0]
		summary = fmt.Sprintf("Successfully recognized: '%s' (%d %s) at confidence %.0f%%.",
			first.ProductName, first.Quantity, first.Unit, first.Confidence.Overall*100)
	} else if len(responseLines) > 0 {
		summary = fmt.Sprintf("Processed audio capture; hold for review (boundary: %s).",
			validationRes.ClosingPhrase)
	} else {
		summary = "Audio captured and processed."
	}

	writeJSON(w, http.StatusOK, testCaptureResponse{
		Status:           "success",
		Recognized:       recognizedAny,
		Type:             validationRes.Type,
		ClosingPhrase:    validationRes.ClosingPhrase,
		BoundaryDetected: validationRes.ClosingPhrase != "",
		Lines:            responseLines,
		Summary:          summary,
		TransactionID:    recordedTxnID,
	})
}
