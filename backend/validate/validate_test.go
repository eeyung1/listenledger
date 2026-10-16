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

func TestExtractSupplier(t *testing.T) {
	tests := []struct {
		input        string
		wantSupplier string
	}{
		{
			input:        "From Dangote: 50 bags sugar 400,000 naira. stock received",
			wantSupplier: "Dangote",
		},
		{
			input:        "Delivery from Chi Limited, 10 cartons chivita 25,000 naira. stock received",
			wantSupplier: "Chi Limited",
		},
		{
			input:        "Supplier Golden Penny: 5 cartons spaghetti. stock received",
			wantSupplier: "Golden Penny",
		},
		{
			input:        "Restock from BUA: 20 bags flour. stock received",
			wantSupplier: "BUA",
		},
		{
			input:        "From Unilever 10 cartons knorr. stock received",
			wantSupplier: "Unilever",
		},
		{
			input:        "2 Milo 4000 naira. thank you",
			wantSupplier: "",
		},
	}

	for _, tc := range tests {
		supp, remaining := validate.ExtractSupplier(tc.input)
		if supp != tc.wantSupplier {
			t.Errorf("ExtractSupplier(%q): got supplier %q, want %q", tc.input, supp, tc.wantSupplier)
		}
		if tc.wantSupplier != "" && remaining == tc.input {
			t.Errorf("ExtractSupplier(%q): remaining text was not stripped", tc.input)
		}
	}
}

func TestDetectCancellation(t *testing.T) {
	tests := []struct {
		input     string
		wantMatch bool
	}{
		{"cancel sale", true},
		{"Customer cancelled the purchase", true},
		{"Please cancel transaction", true},
		{"Never mind, drop that", true},
		{"Cancel that", true},
		{"Two milo 4000 naira thank you", false},
	}

	for _, tc := range tests {
		_, got := validate.DetectCancellation(tc.input)
		if got != tc.wantMatch {
			t.Errorf("DetectCancellation(%q) = %v, want %v", tc.input, got, tc.wantMatch)
		}
	}
}

func TestValidateSpokenCancellationAndSupplier(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// 1. Spoken cancellation aloud
	cancelReq := validate.CaptureRequest{
		CaptureID:  "cap_cancel_01",
		Transcript: "Two milo 4000 naira wait customer cancelled",
	}
	res, err := engine.Validate(context.Background(), cancelReq, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error on cancel validate: %v", err)
	}
	if res.RequiresReview {
		t.Fatalf("cancelled transaction should not require review, got RequiresReview=true")
	}
	if len(res.Lines) == 0 {
		t.Fatalf("expected at least 1 validated line")
	}
	if res.Lines[0].Transaction.Status != models.StatusCancelled {
		t.Fatalf("expected StatusCancelled, got %s", res.Lines[0].Transaction.Status)
	}

	// 2. Spoken restock with supplier
	restockReq := validate.CaptureRequest{
		CaptureID:  "cap_restock_01",
		Transcript: "From Dangote: 10 cartons milo 200,000 naira. stock received",
	}
	res2, err := engine.Validate(context.Background(), restockReq, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error on restock validate: %v", err)
	}
	if len(res2.Lines) == 0 {
		t.Fatalf("expected at least 1 line for restock")
	}
	if res2.Lines[0].Transaction.SupplierName != "Dangote" {
		t.Fatalf("expected supplier Dangote, got %q", res2.Lines[0].Transaction.SupplierName)
	}
}

func TestSplitCompoundUtterance(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{
			input: "two milo and three indomie",
			want:  []string{"two milo", "three indomie"},
		},
		{
			input: "5 peak milk, 2 sugar, plus 1 bread",
			want:  []string{"5 peak milk", "2 sugar", "1 bread"},
		},
		{
			input: "bread and butter",
			want:  []string{"bread and butter"},
		},
		{
			input: "macaroni and cheese",
			want:  []string{"macaroni and cheese"},
		},
		{
			input: "ten cartons of indomie and a carton of biscuits",
			want:  []string{"ten cartons of indomie", "a carton of biscuits"},
		},
		{
			input: "two cartons of indomie with three bottles of coke",
			want:  []string{"two cartons of indomie", "three bottles of coke"},
		},
	}

	for _, tc := range tests {
		got := validate.SplitCompoundUtterance(tc.input)
		if len(got) != len(tc.want) {
			t.Fatalf("SplitCompoundUtterance(%q) length = %d, want %d (%v)", tc.input, len(got), len(tc.want), got)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("SplitCompoundUtterance(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestValidateCompoundMultiItemUtterance(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	req := validate.CaptureRequest{
		CaptureID:  "cap_compound_01",
		Transcript: "two milo and three pepsi thank you",
	}

	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error validating compound utterance: %v", err)
	}

	if len(res.Lines) != 2 {
		t.Fatalf("expected 2 validated lines, got %d", len(res.Lines))
	}

	line1 := res.Lines[0].Transaction
	if line1.ProductName != "Milo 500g" || line1.Quantity != 2 {
		t.Errorf("expected Line 1 to be 2 Milo 500g, got %d %s", line1.Quantity, line1.ProductName)
	}
	if line1.Status != models.StatusConfirmed {
		t.Errorf("expected Line 1 status confirmed, got %s (reasons: %v)", line1.Status, res.Lines[0].ReviewReasons)
	}

	line2 := res.Lines[1].Transaction
	if line2.ProductName != "Pepsi 50cl" || line2.Quantity != 3 {
		t.Errorf("expected Line 2 to be 3 Pepsi 50cl, got %d %s", line2.Quantity, line2.ProductName)
	}
	if line2.Status != models.StatusConfirmed {
		t.Errorf("expected Line 2 status confirmed, got %s (reasons: %v)", line2.Status, res.Lines[1].ReviewReasons)
	}
}
