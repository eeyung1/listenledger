package validate

import (
	"context"
	"testing"

	"github.com/eeyung1/listenledger-backend/models"
)

func TestAdversarialDialogueAndSelfCorrection(t *testing.T) {
	engine := NewEngine()
	catalogue := []models.Product{
		{
			ID:       "prd_milo",
			ShopID:   "shop_adv_1",
			Name:     "Milo",
			SKU:      "MILO-500G",
			BaseUnit: "pack",
			Price:    models.Money{Amount: 200000, Currency: "NGN"},
			Quantity: 50,
			Aliases:  []string{"milo 500g", "chocolate drink"},
		},
		{
			ID:       "prd_peak",
			ShopID:   "shop_adv_1",
			Name:     "Peak Milk",
			SKU:      "PEAK-WMP",
			BaseUnit: "pack",
			Price:    models.Money{Amount: 150000, Currency: "NGN"},
			Quantity: 40,
			Aliases:  []string{"peak", "milk"},
		},
	}

	tests := []struct {
		name             string
		transcript       string
		expectedType     models.TransactionType
		expectedQuantity int
		expectedProduct  string
		expectedReview   bool
	}{
		{
			name:             "Self-correction: wait no",
			transcript:       "Two Milo, wait no, three Milo, six thousand naira. Thank you.",
			expectedType:     models.TypeSale,
			expectedQuantity: 3,
			expectedProduct:  "Milo",
			expectedReview:   false,
		},
		{
			name:             "Self-correction: sorry with digits",
			transcript:       "1 Milo, sorry 4 Milo, eight thousand naira. Thank you.",
			expectedType:     models.TypeSale,
			expectedQuantity: 4,
			expectedProduct:  "Milo",
			expectedReview:   false,
		},
		{
			name:             "Conversational greeting and polite filler",
			transcript:       "Good morning, how are you? Two Milo, please. Four thousand naira. Thank you.",
			expectedType:     models.TypeSale,
			expectedQuantity: 2,
			expectedProduct:  "Milo",
			expectedReview:   false,
		},
		{
			name:             "Speech fillers uh and um",
			transcript:       "Uh, two Peak Milk, um, three thousand naira. Thank you.",
			expectedType:     models.TypeSale,
			expectedQuantity: 2,
			expectedProduct:  "Peak Milk",
			expectedReview:   false,
		},
		{
			name:             "Repeated phrase repetition",
			transcript:       "Two Milo, two Milo, four thousand naira. Thank you.",
			expectedType:     models.TypeSale,
			expectedQuantity: 2,
			expectedProduct:  "Milo",
			expectedReview:   false,
		},
		{
			name:             "Incomplete speech without boundary phrase",
			transcript:       "Two Milo, four thousand naira",
			expectedType:     models.TypeSale,
			expectedQuantity: 2,
			expectedProduct:  "Milo",
			expectedReview:   true, // Missing boundary holds for human review
		},
		{
			name:             "Unrelated counter banter",
			transcript:       "How is the family today? It is very sunny outside.",
			expectedType:     models.TypeSale,
			expectedQuantity: 0,
			expectedProduct:  "",
			expectedReview:   true, // Completely unknown product held for review
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := engine.Validate(
				context.Background(),
				CaptureRequest{
					CaptureID:  "cap_adv",
					Transcript: tc.transcript,
				},
				"shop_adv_1",
				"NGN",
				0.75,
				"Thank you",
				"Stock received",
				catalogue,
			)
			if err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}

			if res.RequiresReview != tc.expectedReview {
				var allReasons []string
				for _, l := range res.Lines {
					allReasons = append(allReasons, l.ReviewReasons...)
					t.Logf("Line: product=%q, qty=%d, amt=%d, reasons=%v", l.Transaction.ProductName, l.Transaction.Quantity, l.Transaction.Amount.Amount, l.ReviewReasons)
				}
				t.Errorf("RequiresReview = %v, want %v. Reasons: %v", res.RequiresReview, tc.expectedReview, allReasons)
			}

			if len(res.Lines) > 0 {
				line := res.Lines[0]
				if tc.expectedProduct != "" && line.Transaction.ProductName != tc.expectedProduct {
					t.Errorf("ProductName = %q, want %q", line.Transaction.ProductName, tc.expectedProduct)
				}
				if line.Transaction.Quantity != tc.expectedQuantity {
					t.Errorf("Quantity = %d, want %d", line.Transaction.Quantity, tc.expectedQuantity)
				}
			}
		})
	}
}
