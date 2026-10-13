package validate_test

import (
	"context"
	"testing"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/validate"
)

func sampleCatalogue() []models.Product {
	return []models.Product{
		{
			ID:       "prd_milo",
			ShopID:   "shop_1",
			Name:     "Milo 500g",
			SKU:      "MILO-500",
			Aliases:  []string{"Milo", "Nestle Milo"},
			Price:    models.Money{Amount: 200000, Currency: "NGN"},
			Quantity: 20,
			BaseUnit: "pack",
			Packaging: []models.PackagingRelation{
				{From: "carton", To: "pack", Factor: 12},
			},
		},
		{
			ID:       "prd_pepsi",
			ShopID:   "shop_1",
			Name:     "Pepsi 50cl",
			SKU:      "PEP-50",
			Aliases:  []string{"Pepsi", "Pepsi bottle"},
			Price:    models.Money{Amount: 240000, Currency: "NGN"},
			Quantity: 8,
			BaseUnit: "pack",
			Packaging: []models.PackagingRelation{
				{From: "carton", To: "pack", Factor: 12},
			},
		},
		{
			ID:       "prd_peak",
			ShopID:   "shop_1",
			Name:     "Peak Milk 400g",
			SKU:      "PEAK-400",
			Aliases:  []string{"Peak", "Peak tin", "Peak milk", "Big Peak"},
			Price:    models.Money{Amount: 350000, Currency: "NGN"},
			Quantity: 15,
			BaseUnit: "tin",
		},
	}
}

func TestCatalogueMatcher(t *testing.T) {
	catalogue := sampleCatalogue()

	// 1. Exact SKU
	res := validate.MatchProduct("MILO-500", catalogue)
	if res.Product == nil || res.Product.ID != "prd_milo" || res.Confidence != 1.0 {
		t.Fatalf("expected exact SKU match with 1.0, got %+v", res)
	}

	// 2. Exact Name
	res = validate.MatchProduct("Peak Milk 400g", catalogue)
	if res.Product == nil || res.Product.ID != "prd_peak" || res.Confidence != 0.98 {
		t.Fatalf("expected exact Name match with 0.98, got %+v", res)
	}

	// 3. Configured Alias: "Big Peak"
	res = validate.MatchProduct("Big Peak", catalogue)
	if res.Product == nil || res.Product.ID != "prd_peak" || res.Confidence != 0.95 {
		t.Fatalf("expected alias match for 'Big Peak' with 0.95, got %+v", res)
	}

	// 4. Fuzzy match: "Milo 500"
	res = validate.MatchProduct("Milo 500", catalogue)
	if res.Product == nil || res.Product.ID != "prd_milo" || res.Confidence < 0.80 {
		t.Fatalf("expected fuzzy match for 'Milo 500', got %+v", res)
	}

	// 5. Unknown product
	res = validate.MatchProduct("Golden Morn Cereal", catalogue)
	if res.Product != nil || res.Confidence != 0.0 {
		t.Fatalf("expected unknown product match with 0.0, got %+v", res)
	}
}

func TestBlueprintExample1_Sale(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// Blueprint §1: "Two Milo, four thousand naira. Thank you."
	req := validate.CaptureRequest{
		CaptureID:  "cap_sale_001",
		Transcript: "Two Milo, four thousand naira. Thank you.",
	}

	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Type != models.TypeSale {
		t.Fatalf("expected TypeSale, got %v", res.Type)
	}
	if len(res.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(res.Lines))
	}

	line := res.Lines[0]
	if line.Transaction.ProductID != "prd_milo" {
		t.Errorf("expected product prd_milo, got %s", line.Transaction.ProductID)
	}
	if line.Transaction.Quantity != 2 {
		t.Errorf("expected quantity 2, got %d", line.Transaction.Quantity)
	}
	if line.Transaction.Amount.Amount != 400000 { // 4,000 NGN in kobo
		t.Errorf("expected amount 400000, got %d", line.Transaction.Amount.Amount)
	}
	if line.Transaction.Status != models.StatusConfirmed {
		t.Errorf("expected status confirmed, got %s (reasons: %v)", line.Transaction.Status, line.ReviewReasons)
	}
	if res.RequiresReview {
		t.Errorf("expected RequiresReview false for clean sale")
	}
}

func TestBlueprintExample10_RestockMultiLine(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// Blueprint §10: “Five packs of Pepsi, twelve thousand naira. Three cartons of Milo, forty-five thousand naira. Stock received.”
	req := validate.CaptureRequest{
		CaptureID:  "cap_restock_001",
		Transcript: "Five packs of Pepsi, twelve thousand naira. Three cartons of Milo, forty-five thousand naira. Stock received.",
	}

	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Type != models.TypeRestock {
		t.Fatalf("expected TypeRestock, got %v", res.Type)
	}
	if len(res.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(res.Lines))
	}

	// Line 1: 5 packs of Pepsi, 12,000 NGN
	l1 := res.Lines[0]
	if l1.Transaction.ProductID != "prd_pepsi" {
		t.Errorf("expected prd_pepsi, got %s", l1.Transaction.ProductID)
	}
	if l1.Transaction.Quantity != 5 {
		t.Errorf("expected quantity 5, got %d", l1.Transaction.Quantity)
	}
	if l1.Transaction.Unit != "pack" {
		t.Errorf("expected unit pack, got %s", l1.Transaction.Unit)
	}
	if l1.Transaction.Amount.Amount != 1200000 {
		t.Errorf("expected 1200000 kobo, got %d", l1.Transaction.Amount.Amount)
	}
	if l1.Transaction.Status != models.StatusConfirmed {
		t.Errorf("expected line 1 confirmed, got %s (%v)", l1.Transaction.Status, l1.ReviewReasons)
	}

	// Line 2: 3 cartons of Milo, 45,000 NGN
	l2 := res.Lines[1]
	if l2.Transaction.ProductID != "prd_milo" {
		t.Errorf("expected prd_milo, got %s", l2.Transaction.ProductID)
	}
	if l2.Transaction.Quantity != 3 {
		t.Errorf("expected quantity 3, got %d", l2.Transaction.Quantity)
	}
	if l2.Transaction.Unit != "carton" {
		t.Errorf("expected unit carton, got %s", l2.Transaction.Unit)
	}
	if l2.Transaction.Amount.Amount != 4500000 {
		t.Errorf("expected 4500000 kobo, got %d", l2.Transaction.Amount.Amount)
	}
	if l2.Transaction.Status != models.StatusConfirmed {
		t.Errorf("expected line 2 confirmed, got %s (%v)", l2.Transaction.Status, l2.ReviewReasons)
	}
}

func TestBlueprintExample11_LowConfidenceHoldsForReview(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// Unknown product "Golden Morn"
	req := validate.CaptureRequest{
		CaptureID:  "cap_unknown_001",
		Transcript: "Two Golden Morn, five thousand naira. Thank you.",
	}

	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !res.RequiresReview {
		t.Fatalf("expected capture with unknown product to require review")
	}
	if res.Lines[0].Transaction.Status != models.StatusReviewRequired {
		t.Fatalf("expected StatusReviewRequired, got %s", res.Lines[0].Transaction.Status)
	}
	if res.Lines[0].Transaction.ProductID != "" {
		t.Fatalf("expected empty ProductID for unmapped item, got %s", res.Lines[0].Transaction.ProductID)
	}
}

func TestMissingBoundaryPhraseHoldsForReview(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// Missing closing phrase "Thank you"
	req := validate.CaptureRequest{
		CaptureID:  "cap_incomplete_001",
		Transcript: "Two Milo, four thousand naira",
	}

	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !res.RequiresReview {
		t.Fatalf("expected missing closing phrase to hold for review")
	}
	if res.Lines[0].Transaction.Status != models.StatusReviewRequired {
		t.Fatalf("expected StatusReviewRequired, got %s", res.Lines[0].Transaction.Status)
	}
}

func TestAmbiguousAliasHoldsForReview(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := []models.Product{
		{
			ID:       "prd_1",
			ShopID:   "shop_1",
			Name:     "Peak Milk Powder 400g",
			SKU:      "PEAK-POW-400",
			Aliases:  []string{"Peak", "Common Milk"},
			Price:    models.Money{Amount: 300000, Currency: "NGN"},
			Quantity: 10,
			BaseUnit: "tin",
		},
		{
			ID:       "prd_2",
			ShopID:   "shop_1",
			Name:     "Peak Evaporated Milk 160g",
			SKU:      "PEAK-EVAP-160",
			Aliases:  []string{"Peak", "Common Milk"},
			Price:    models.Money{Amount: 150000, Currency: "NGN"},
			Quantity: 10,
			BaseUnit: "tin",
		},
	}

	req := validate.CaptureRequest{
		CaptureID:  "cap_ambig_001",
		Transcript: "One Common Milk, three thousand naira. Thank you.",
	}

	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !res.RequiresReview {
		t.Fatalf("expected ambiguous alias match to require review")
	}
	if res.Lines[0].Transaction.Status != models.StatusReviewRequired {
		t.Fatalf("expected StatusReviewRequired, got %s", res.Lines[0].Transaction.Status)
	}
}

func TestDigitsAndCurrencyFormatting(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	req := validate.CaptureRequest{
		CaptureID:  "cap_num_001",
		Transcript: "2 Milo, 4,000 NGN. Thank you.",
	}

	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.RequiresReview {
		t.Fatalf("expected clean confirmation for digits and NGN notation, got review reasons: %v", res.Lines[0].ReviewReasons)
	}
	if res.Lines[0].Transaction.Quantity != 2 || res.Lines[0].Transaction.Amount.Amount != 400000 {
		t.Fatalf("expected 2 qty and 400000 amount, got %d and %d", res.Lines[0].Transaction.Quantity, res.Lines[0].Transaction.Amount.Amount)
	}
}
