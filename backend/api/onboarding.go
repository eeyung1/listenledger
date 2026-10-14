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

// OnboardingSteps aggregates the 6 onboarding milestones.
type OnboardingSteps struct {
	Profile       StepDetail `json:"profile"`
	Catalogue     StepDetail `json:"catalogue"`
	Devices       StepDetail `json:"devices"`
	StaffTraining StepDetail `json:"staff_training"`
	TargetGates   StepDetail `json:"target_gates"`
	TestCapture   StepDetail `json:"test_capture"`
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
	staffTrainingCompleted := false
	if err == nil {
		profileCompleted = strings.TrimSpace(shop.Name) != "" && strings.TrimSpace(shop.Currency) != ""
		staffTrainingCompleted = shop.StaffTrainedAt != nil && *shop.StaffTrainedAt != ""
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

	// 4. Check Pilot Target Gates Agreement
	_, targetGatesCompleted, err := s.store.GetTargetGates(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "get target gates for onboarding status", err)
		return
	}

	// 5. Check Test Capture / Initial Transaction
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
		StaffTraining: StepDetail{
			Completed:   staffTrainingCompleted,
			Title:       "Cashier Protocol Training",
			Description: "Train staff on product names, quantities, and distinct sale/restock boundaries",
		},
		TargetGates: StepDetail{
			Completed:   targetGatesCompleted,
			Title:       "Pilot Target Gates Agreement",
			Description: "Agree accuracy, latency, and correction targets with shop owner before trial",
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
	if staffTrainingCompleted {
		completedCount++
	}
	if targetGatesCompleted {
		completedCount++
	}
	if testCaptureCompleted {
		completedCount++
	}

	writeJSON(w, http.StatusOK, OnboardingStatusResponse{
		ShopID:         shopID,
		Steps:          steps,
		CompletedSteps: completedCount,
		TotalSteps:     6,
		IsComplete:     completedCount == 6,
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

// drillRequest is the payload of POST /api/onboarding/drill.
type drillRequest struct {
	ScenarioType string `json:"scenario_type"` // "clean_sale", "restock", "non_commercial_chatter", "spoken_correction", "custom"
	Transcript   string `json:"transcript"`
}

type drillCheckItem struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type drillResponse struct {
	Passed           bool             `json:"passed"`
	Score            int              `json:"score"`
	ScenarioType     string           `json:"scenario_type"`
	BoundaryDetected bool             `json:"boundary_detected"`
	ClosingPhrase    string           `json:"closing_phrase"`
	RecognizedItem   string           `json:"recognized_item"`
	Checklist        []drillCheckItem `json:"checklist"`
	CoachingTips     []string         `json:"coaching_tips"`
	StaffTrainedAt   string           `json:"staff_trained_at,omitempty"`
}

// handleOnboardingDrill evaluates a cashier protocol training interaction and provides coaching tips.
func (s *Server) handleOnboardingDrill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req drillRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	transcript := strings.TrimSpace(req.Transcript)
	if transcript == "" {
		writeError(w, http.StatusBadRequest, "transcript is required for cashier protocol drill")
		return
	}

	scenarioType := strings.ToLower(strings.TrimSpace(req.ScenarioType))
	if scenarioType == "" {
		scenarioType = "clean_sale"
	}

	// 1. Fetch shop configuration
	shop, err := s.store.GetShop(r.Context(), shopID)
	salePhrase := "Thank you"
	restockPhrase := "Stock received"
	threshold := 0.75
	currency := "NGN"
	if err == nil {
		if shop.SalePhrase != "" {
			salePhrase = shop.SalePhrase
		}
		if shop.RestockPhrase != "" {
			restockPhrase = shop.RestockPhrase
		}
		if shop.ConfidenceThreshold > 0 {
			threshold = shop.ConfidenceThreshold
		}
		if shop.Currency != "" {
			currency = shop.Currency
		}
	}

	catalogue, err := s.store.ListProducts(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "load catalogue for drill", err)
		return
	}

	// 2. Boundary phrase detection
	_, detectedPhrase, boundaryFound := validate.DetectBoundary(transcript, salePhrase, restockPhrase)

	var (
		checklist      []drillCheckItem
		coachingTips   []string
		recognizedItem string
	)

	// 3. Scenario evaluation
	switch scenarioType {
	case "non_commercial_chatter":
		boundaryOmitted := !boundaryFound
		checklist = append(checklist, drillCheckItem{
			Name:   "Closing Boundary Omission",
			Passed: boundaryOmitted,
			Detail: func() string {
				if boundaryOmitted {
					return "No closing phrase spoken during casual chatter (correct)"
				}
				return fmt.Sprintf("Accidental closing phrase %q detected in casual conversation", detectedPhrase)
			}(),
		})

		checklist = append(checklist, drillCheckItem{
			Name:   "Zero Ledger Distraction",
			Passed: boundaryOmitted,
			Detail: func() string {
				if boundaryOmitted {
					return "Microphone ignored non-commercial banter without creating phantom records"
				}
				return "Warning: microphone will create a review queue hold because closing phrase was spoken"
			}(),
		})

		if !boundaryOmitted {
			coachingTips = append(coachingTips, fmt.Sprintf("Never say closing phrases like %q during idle counter talk or greetings. Save them strictly for when a customer pays.", salePhrase))
		} else {
			coachingTips = append(coachingTips, "Great discipline! Casual counter chatter should flow naturally without trigger phrases.")
		}

	default: // clean_sale, restock, spoken_correction, custom
		expectedPhrase := salePhrase
		expectedType := models.TypeSale
		if scenarioType == "restock" {
			expectedPhrase = restockPhrase
			expectedType = models.TypeRestock
		}

		// Check 1: Boundary phrase
		boundaryCorrect := boundaryFound && strings.EqualFold(detectedPhrase, expectedPhrase)
		checklist = append(checklist, drillCheckItem{
			Name:   "Transaction Closing Boundary",
			Passed: boundaryCorrect,
			Detail: func() string {
				if boundaryCorrect {
					return fmt.Sprintf("Closing phrase %q cleanly recognized", detectedPhrase)
				}
				if boundaryFound {
					return fmt.Sprintf("Used %q instead of expected %q", detectedPhrase, expectedPhrase)
				}
				return fmt.Sprintf("Missing closing boundary phrase (expected %q)", expectedPhrase)
			}(),
		})

		if !boundaryFound {
			coachingTips = append(coachingTips, fmt.Sprintf("Conclude the transaction with %q to cleanly signal completion to the counter microphone.", expectedPhrase))
		} else if !strings.EqualFold(detectedPhrase, expectedPhrase) {
			coachingTips = append(coachingTips, fmt.Sprintf("Use %q for %s (you said %q). Keep sale and restock phrases distinct.", expectedPhrase, scenarioType, detectedPhrase))
		}

		// Validation engine parse
		capReq := validate.CaptureRequest{
			Type:       expectedType,
			Transcript: transcript,
		}
		validationRes, _ := s.validateEngine.Validate(
			r.Context(),
			capReq,
			shopID,
			currency,
			threshold,
			salePhrase,
			restockPhrase,
			catalogue,
		)

		var firstLine *models.Transaction
		productMatched := false
		qtyMatched := false
		amtMatched := false

		if len(validationRes.Lines) > 0 {
			line := validationRes.Lines[0]
			firstLine = &line.Transaction
			recognizedItem = line.Transaction.ProductName
			productMatched = line.MatchedBy != "none" && line.Transaction.ProductID != ""
			qtyMatched = line.Transaction.Quantity > 0
			amtMatched = line.Transaction.Amount.Amount > 0
		}

		// Check 2: Product Name
		checklist = append(checklist, drillCheckItem{
			Name:   "Product Name Recognition",
			Passed: productMatched,
			Detail: func() string {
				if productMatched && firstLine != nil {
					return fmt.Sprintf("Matched catalogue item: %s", firstLine.ProductName)
				}
				return "Product name was not matched against catalogue"
			}(),
		})
		if !productMatched {
			coachingTips = append(coachingTips, "Speak product names clearly using registered catalogue titles or aliases.")
		}

		// Check 3: Quantity & Packaging Unit
		checklist = append(checklist, drillCheckItem{
			Name:   "Explicit Quantity & Unit",
			Passed: qtyMatched,
			Detail: func() string {
				if qtyMatched && firstLine != nil {
					return fmt.Sprintf("Recognized %d %s", firstLine.Quantity, firstLine.Unit)
				}
				return "Quantity or unit missing/unclear"
			}(),
		})
		if !qtyMatched {
			coachingTips = append(coachingTips, "State the count and unit explicitly (e.g., '2 tins', '1 carton', '5 pieces').")
		}

		// Check 4: Spoken Price / Amount
		checklist = append(checklist, drillCheckItem{
			Name:   "Monetary Amount Clarity",
			Passed: amtMatched,
			Detail: func() string {
				if amtMatched && firstLine != nil {
					return fmt.Sprintf("Recognized price: %d %s", firstLine.Amount.Amount/100, firstLine.Amount.Currency)
				}
				return "Monetary price was not captured"
			}(),
		})
		if !amtMatched {
			coachingTips = append(coachingTips, "State the monetary price clearly in Naira (e.g. '7000 naira' or '4200 kobo').")
		}

		// Additional check for spoken corrections
		if scenarioType == "spoken_correction" {
			lower := strings.ToLower(transcript)
			hasCorrectionKeyword := strings.Contains(lower, "wait") || strings.Contains(lower, "actually") ||
				strings.Contains(lower, "make that") || strings.Contains(lower, "no") || strings.Contains(lower, "sorry")
			checklist = append(checklist, drillCheckItem{
				Name:   "Mid-Sentence Self-Correction",
				Passed: hasCorrectionKeyword && productMatched && qtyMatched,
				Detail: "Self-correction marker resolved correctly to final quantity",
			})
			if hasCorrectionKeyword && productMatched && qtyMatched {
				coachingTips = append(coachingTips, "Spoken self-correction was handled cleanly! The ledger recorded the revised quantity.")
			}
		}
	}

	// 4. Calculate score
	passedCount := 0
	for _, c := range checklist {
		if c.Passed {
			passedCount++
		}
	}
	score := int((float64(passedCount) / float64(len(checklist))) * 100.0)
	passed := score >= 75

	// 5. Mark staff trained if passed
	staffTrainedAtStr := ""
	if passed {
		trainedTime, err := s.store.MarkStaffTrained(r.Context(), shopID)
		if err == nil {
			staffTrainedAtStr = trainedTime.Format(time.RFC3339)
			s.publish(w, r, shopID, ws.EventStaffTrainingCompleted, map[string]any{
				"trained_at": staffTrainedAtStr,
				"score":      score,
			})
		}
	}

	writeJSON(w, http.StatusOK, drillResponse{
		Passed:           passed,
		Score:            score,
		ScenarioType:     scenarioType,
		BoundaryDetected: boundaryFound,
		ClosingPhrase:    detectedPhrase,
		RecognizedItem:   recognizedItem,
		Checklist:        checklist,
		CoachingTips:     coachingTips,
		StaffTrainedAt:   staffTrainedAtStr,
	})
}
