package validate

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
)

// DefaultReviewThreshold applies when a shop has not configured an explicit threshold.
const DefaultReviewThreshold = 0.75

// CaptureLineInput represents pre-extracted or client-supplied structured anchors for one line.
type CaptureLineInput struct {
	ProductID   string             `json:"product_id,omitempty"`
	ProductName string             `json:"product_name"`
	Quantity    int                `json:"quantity"`
	Unit        models.Unit        `json:"unit"`
	Amount      *models.Money      `json:"amount,omitempty"`
	Confidence  *models.Confidence `json:"confidence,omitempty"`
}

// CaptureRequest is the edge intake payload for POST /api/captures.
type CaptureRequest struct {
	CaptureID      string                 `json:"capture_id"`
	DeviceID       string                 `json:"device_id,omitempty"`
	Type           models.TransactionType `json:"type,omitempty"`
	Transcript     string                 `json:"transcript"`
	ClosingPhrase  string                 `json:"closing_phrase,omitempty"`
	OccurredAt     *time.Time             `json:"occurred_at,omitempty"`
	ExtractedLines []CaptureLineInput     `json:"extracted_lines,omitempty"`
}

// ValidatedLine contains the fully validated transaction ready for storage.
type ValidatedLine struct {
	Transaction   models.Transaction
	ReviewReasons []string
	MatchedBy     string
	RawAnchor     CaptureLineInput
}

// ValidationResult summarizes the recognition and validation outcome across all lines.
type ValidationResult struct {
	CaptureID      string
	Type           models.TransactionType
	ClosingPhrase  string
	Lines          []ValidatedLine
	RequiresReview bool
}

// Engine encapsulates catalogue matching, transcript parsing, and confidence evaluation.
type Engine struct{}

// NewEngine constructs a validation engine.
func NewEngine() *Engine {
	return &Engine{}
}

// Validate processes a capture request against a shop's catalogue and thresholds.
func (e *Engine) Validate(
	_ context.Context,
	req CaptureRequest,
	shopID string,
	currency string,
	threshold float64,
	salePhrase string,
	restockPhrase string,
	catalogue []models.Product,
) (ValidationResult, error) {
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultReviewThreshold
	}
	if currency == "" {
		currency = "NGN"
	}

	// 1. Determine Transaction Type and Boundary Phrase
	txnType := req.Type
	closingPhrase := req.ClosingPhrase
	boundaryFound := false
	boundaryConf := 1.0

	if txnType == "" || closingPhrase == "" {
		detectedType, detectedPhrase, found := DetectBoundary(req.Transcript, salePhrase, restockPhrase)
		if found {
			if txnType == "" {
				txnType = detectedType
			}
			closingPhrase = detectedPhrase
			boundaryFound = true
		} else {
			if txnType == "" {
				txnType = models.TypeSale // default assumption
			}
			boundaryConf = 0.0
		}
	} else {
		boundaryFound = true
	}

	occurredAt := time.Now().UTC()
	if req.OccurredAt != nil {
		occurredAt = req.OccurredAt.UTC()
	}

	// 2. Prepare Line Items (either from pre-extracted lines or by parsing transcript)
	var linesToProcess []CaptureLineInput
	if len(req.ExtractedLines) > 0 {
		linesToProcess = req.ExtractedLines
	} else {
		splitSegments := SplitTranscriptLines(req.Transcript, closingPhrase)
		for _, seg := range splitSegments {
			anchors := ParseSpokenSegment(seg, currency)
			if anchors.ProductMention == "" && anchors.Quantity == 0 && anchors.AmountMinor == 0 {
				continue
			}
			linesToProcess = append(linesToProcess, CaptureLineInput{
				ProductName: anchors.ProductMention,
				Quantity:    anchors.Quantity,
				Unit:        anchors.Unit,
				Amount: &models.Money{
					Amount:   anchors.AmountMinor,
					Currency: anchors.Currency,
				},
			})
		}
	}

	if len(linesToProcess) == 0 {
		// Even empty or unintelligible speech is held rather than ignored
		linesToProcess = append(linesToProcess, CaptureLineInput{
			ProductName: "Unintelligible speech",
			Quantity:    0,
			Unit:        models.Unit("piece"),
			Amount:      &models.Money{Amount: 0, Currency: currency},
		})
	}

	var validatedLines []ValidatedLine
	anyReview := false

	// 3. Validate each line
	for i, line := range linesToProcess {
		var reviewReasons []string

		// Product Matching
		candidateProduct := strings.TrimSpace(line.ProductName)
		match := MatchProduct(candidateProduct, catalogue)
		productConf := match.Confidence

		productID := ""
		productName := candidateProduct
		unit := line.Unit

		if match.Product != nil {
			productID = match.Product.ID
			productName = match.Product.Name
			if unit == "" || !unit.Valid() {
				unit = models.Unit(match.Product.BaseUnit)
			}
		}
		if match.Ambiguous {
			reviewReasons = append(reviewReasons, "ambiguous_product_match")
		}
		if match.Product == nil || productConf < 0.70 {
			reviewReasons = append(reviewReasons, "unknown_product")
		}

		// Quantity Validation
		qty := line.Quantity
		qtyConf := 0.98
		if qty <= 0 {
			qtyConf = 0.0
			reviewReasons = append(reviewReasons, "missing_or_invalid_quantity")
		} else if qty > 10000 {
			qtyConf = 0.50
			reviewReasons = append(reviewReasons, "implausibly_large_quantity")
		}

		// Unit Validation
		if unit == "" || !unit.Valid() {
			unit = models.Unit("piece")
			reviewReasons = append(reviewReasons, "unrecognized_or_missing_unit")
		}

		// Amount Validation
		amountVal := int64(0)
		amountCurr := currency
		if line.Amount != nil {
			amountVal = line.Amount.Amount
			if line.Amount.Currency != "" {
				amountCurr = line.Amount.Currency
			}
		}

		amountConf := 0.95
		if amountVal <= 0 {
			if txnType == models.TypeRestock {
				// Spoken restock receipts may omit wholesale pricing
				amountConf = 0.95
			} else if match.Product != nil && match.Product.Price.Amount > 0 {
				// Blueprint §10: default to catalogue unit price when spoken price is omitted
				amountVal = int64(qty) * match.Product.Price.Amount
				amountConf = 0.95
			} else {
				amountConf = 0.0
				reviewReasons = append(reviewReasons, "missing_or_invalid_amount")
			}
		} else if match.Product != nil && txnType == models.TypeSale && match.Product.Price.Amount > 0 {
			expected := int64(qty) * match.Product.Price.Amount
			diffRatio := math.Abs(float64(amountVal-expected)) / float64(expected)
			if diffRatio <= 0.05 {
				amountConf = 0.98
			} else if diffRatio <= 0.25 {
				amountConf = 0.90
			} else {
				amountConf = 0.60
				reviewReasons = append(reviewReasons, fmt.Sprintf("amount_differs_from_catalogue_price_expected_%d", expected))
			}
		}

		// Boundary Check
		if !boundaryFound {
			reviewReasons = append(reviewReasons, "missing_closing_phrase")
		}

		// Overall Confidence: Weakest Anchor Decides (Blueprint §11)
		overallConf := minScore(productConf, qtyConf, amountConf, boundaryConf)

		// Client override of confidence is rejected; engine computes canonical scores
		conf := models.Confidence{
			Overall:  overallConf,
			Product:  &productConf,
			Quantity: &qtyConf,
			Amount:   &amountConf,
		}

		// Status Decision
		status := models.StatusConfirmed
		if overallConf < threshold || productID == "" || len(reviewReasons) > 0 {
			status = models.StatusReviewRequired
			anyReview = true
		}

		txnID := fmt.Sprintf("txn_cap_%s_%d", req.CaptureID, i)
		if len(req.CaptureID) == 0 {
			txnID = fmt.Sprintf("txn_%d_%d", time.Now().UnixNano(), i)
		}

		transcriptText := req.Transcript
		if len(reviewReasons) > 0 {
			transcriptText = fmt.Sprintf("%s [Review Reason: %s]", transcriptText, strings.Join(reviewReasons, ", "))
		}

		txn := models.Transaction{
			ID:          txnID,
			ShopID:      shopID,
			Type:        txnType,
			ProductID:   productID,
			ProductName: productName,
			Quantity:    qty,
			Unit:        unit,
			Amount: models.Money{
				Amount:   amountVal,
				Currency: amountCurr,
			},
			Confidence: conf,
			Status:     status,
			OccurredAt: occurredAt,
			CreatedAt:  time.Now().UTC(),
			Transcript: transcriptText,
		}

		validatedLines = append(validatedLines, ValidatedLine{
			Transaction:   txn,
			ReviewReasons: reviewReasons,
			MatchedBy:     match.MatchedBy,
			RawAnchor:     line,
		})
	}

	return ValidationResult{
		CaptureID:      req.CaptureID,
		Type:           txnType,
		ClosingPhrase:  closingPhrase,
		Lines:          validatedLines,
		RequiresReview: anyReview,
	}, nil
}

func minScore(scores ...float64) float64 {
	m := 1.0
	for _, s := range scores {
		if s < m {
			m = s
		}
	}
	return m
}
