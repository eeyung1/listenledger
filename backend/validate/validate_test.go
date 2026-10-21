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

func TestExtractCustomer(t *testing.T) {
	tests := []struct {
		input        string
		wantCustomer string
		wantCredit   bool
	}{
		{
			input:        "two milo for Emeka on credit thank you",
			wantCustomer: "Emeka",
			wantCredit:   true,
		},
		{
			input:        "three peak milk to Mama Ngozi on tab thank you",
			wantCustomer: "Mama Ngozi",
			wantCredit:   true,
		},
		{
			input:        "two milo on credit for Emeka thank you",
			wantCustomer: "Emeka",
			wantCredit:   true,
		},
		{
			input:        "Put on Emeka's tab: 2 Milo 500 naira",
			wantCustomer: "Emeka",
			wantCredit:   true,
		},
		{
			input:        "Credit sale for Baba Tunde: 5 Indomie",
			wantCustomer: "Baba Tunde",
			wantCredit:   true,
		},
		{
			input:        "Customer: Chinedu, 2 Milo thank you",
			wantCustomer: "Chinedu",
			wantCredit:   false,
		},
		{
			input:        "2 Milo 500 naira on credit thank you",
			wantCustomer: "",
			wantCredit:   true,
		},
	}

	for _, tc := range tests {
		cust, isCredit, _ := validate.ExtractCustomer(tc.input)
		if cust != tc.wantCustomer {
			t.Errorf("ExtractCustomer(%q) customer = %q, want %q", tc.input, cust, tc.wantCustomer)
		}
		if isCredit != tc.wantCredit {
			t.Errorf("ExtractCustomer(%q) isCredit = %v, want %v", tc.input, isCredit, tc.wantCredit)
		}
	}
}

func TestMatchCustomer(t *testing.T) {
	customers := []models.Customer{
		{
			ID:   "cust_1",
			Name: "Emeka Okafor",
		},
		{
			ID:   "cust_2",
			Name: "Mama Ngozi",
		},
		{
			ID:   "cust_3",
			Name: "Baba Tunde",
		},
	}

	// Exact Name match
	m1 := validate.MatchCustomer("Mama Ngozi", customers)
	if m1.Customer == nil || m1.Customer.ID != "cust_2" || m1.Confidence < 0.95 {
		t.Errorf("expected exact match for Mama Ngozi, got %+v", m1)
	}

	// Token match: "Emeka" matches "Emeka Okafor"
	m2 := validate.MatchCustomer("Emeka", customers)
	if m2.Customer == nil || m2.Customer.ID != "cust_1" || m2.Confidence < 0.90 {
		t.Errorf("expected token match for Emeka -> Emeka Okafor, got %+v", m2)
	}

	// Unknown customer
	m3 := validate.MatchCustomer("Unknown Guy", customers)
	if m3.Customer != nil || m3.Confidence > 0.50 {
		t.Errorf("expected no match for Unknown Guy, got %+v", m3)
	}
}

func TestValidateCustomerCreditAttribution(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()
	customers := []models.Customer{
		{
			ID:   "cust_emeka",
			Name: "Emeka Okafor",
		},
	}

	// 1. Matched customer on credit sale
	req1 := validate.CaptureRequest{
		CaptureID:  "cap_credit_01",
		Transcript: "two milo for Emeka on credit thank you",
	}
	res1, err := engine.Validate(context.Background(), req1, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue, customers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res1.IsCreditSale {
		t.Errorf("expected IsCreditSale true, got false")
	}
	if res1.CustomerID != "cust_emeka" || res1.CustomerName != "Emeka Okafor" {
		t.Errorf("expected attributed to Emeka Okafor (cust_emeka), got %s (%s)", res1.CustomerName, res1.CustomerID)
	}
	if len(res1.Lines) != 1 || res1.Lines[0].Transaction.Status != models.StatusConfirmed {
		t.Errorf("expected 1 confirmed line, got %+v", res1.Lines)
	}

	// 2. Unmatched customer on credit sale -> review required
	req2 := validate.CaptureRequest{
		CaptureID:  "cap_credit_02",
		Transcript: "two milo for Stranger on credit thank you",
	}
	res2, err := engine.Validate(context.Background(), req2, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue, customers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res2.IsCreditSale {
		t.Errorf("expected IsCreditSale true, got false")
	}
	if !res2.RequiresReview {
		t.Errorf("expected review required for unmatched credit customer, got false")
	}
	if len(res2.Lines) != 1 || res2.Lines[0].Transaction.Status != models.StatusReviewRequired {
		t.Errorf("expected line status review_required, got %s", res2.Lines[0].Transaction.Status)
	}
}

func TestNigerianMarketColloquialNumbersAndUnits(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := []models.Product{
		{
			ID:       "prd_peak",
			ShopID:   "shop_1",
			Name:     "Peak Milk 400g",
			SKU:      "PEAK-400",
			Aliases:  []string{"Peak", "Peak milk", "Peak tin"},
			Price:    models.Money{Amount: 200000, Currency: "NGN"}, // 2,000 NGN
			Quantity: 20,
			BaseUnit: "tin",
		},
		{
			ID:       "prd_milo",
			ShopID:   "shop_1",
			Name:     "Milo 500g",
			SKU:      "MILO-500",
			Aliases:  []string{"Milo", "Nestle Milo"},
			Price:    models.Money{Amount: 138889, Currency: "NGN"},
			Quantity: 15,
			BaseUnit: "pack",
			Packaging: []models.PackagingRelation{
				{From: "carton", To: "pack", Factor: 12},
			},
		},
		{
			ID:       "prd_indomie",
			ShopID:   "shop_1",
			Name:     "Indomie Instant Noodles",
			SKU:      "IND-70G",
			Aliases:  []string{"Indomie", "Indomie noodles"},
			Price:    models.Money{Amount: 41666, Currency: "NGN"},
			Quantity: 100,
			BaseUnit: "piece",
			Packaging: []models.PackagingRelation{
				{From: "carton", To: "piece", Factor: 40},
			},
		},
		{
			ID:       "prd_garri",
			ShopID:   "shop_1",
			Name:     "Ijebu Garri",
			SKU:      "GARRI-IJEBU",
			Aliases:  []string{"garri", "ijebu garri", "white garri"},
			Price:    models.Money{Amount: 300000, Currency: "NGN"}, // 3,000 NGN per painter
			Quantity: 50,
			BaseUnit: "painter",
			Packaging: []models.PackagingRelation{
				{From: "bag", To: "painter", Factor: 15},
				{From: "painter", To: "mudu", Factor: 4},
			},
		},
	}

	// 1. "Two k Peak milk thank you" -> 2,000 NGN (200,000 kobo)
	req1 := validate.CaptureRequest{
		CaptureID:  "cap_colloquial_01",
		Transcript: "Two k Peak milk thank you",
	}
	res1, err := engine.Validate(context.Background(), req1, "shop_1", "NGN", 0.75, "thank you", "stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if len(res1.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(res1.Lines))
	}
	l1 := res1.Lines[0].Transaction
	if l1.ProductID != "prd_peak" {
		t.Errorf("expected product prd_peak, got %q", l1.ProductID)
	}
	if l1.Amount.Amount != 200000 { // 2,000 NGN = 200,000 kobo
		t.Errorf("expected amount 200000 kobo (2,000 NGN), got %d", l1.Amount.Amount)
	}
	if l1.Quantity != 1 {
		t.Errorf("expected quantity 1, got %d", l1.Quantity)
	}
	// P5.5: "Two k" is the price (₦2,000), so the quantity 1 was inferred rather
	// than spoken. It must not confirm at a spoken quantity's 0.98.
	if l1.Status != models.StatusReviewRequired {
		t.Errorf("expected status review_required for an inferred quantity, got %s (reasons: %v)", l1.Status, res1.Lines[0].ReviewReasons)
	}
	if q := l1.Confidence.Quantity; q == nil || *q >= 0.75 {
		t.Errorf("expected inferred quantity confidence below the review threshold, got %v", q)
	}

	// 2. "Three cartons Milo k-fifty thank you" -> quantity 3, carton, 50,000 NGN (5,000,000 kobo)
	req2 := validate.CaptureRequest{
		CaptureID:  "cap_colloquial_02",
		Transcript: "Three cartons Milo k-fifty thank you",
	}
	res2, err := engine.Validate(context.Background(), req2, "shop_1", "NGN", 0.75, "thank you", "stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if len(res2.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(res2.Lines))
	}
	l2 := res2.Lines[0].Transaction
	if l2.ProductID != "prd_milo" {
		t.Errorf("expected product prd_milo, got %q", l2.ProductID)
	}
	if l2.Quantity != 3 {
		t.Errorf("expected quantity 3, got %d", l2.Quantity)
	}
	if l2.Unit != "carton" {
		t.Errorf("expected unit carton, got %q", l2.Unit)
	}
	if l2.Amount.Amount != 5000000 { // 50,000 NGN = 5,000,000 kobo
		t.Errorf("expected amount 5000000 kobo (50,000 NGN), got %d", l2.Amount.Amount)
	}
	if l2.Status != models.StatusConfirmed {
		t.Errorf("expected status confirmed, got %s (reasons: %v)", l2.Status, res2.Lines[0].ReviewReasons)
	}

	// 3. "Half dozen Indomie two-five thank you" -> quantity 6, 2,500 NGN (250,000 kobo)
	req3 := validate.CaptureRequest{
		CaptureID:  "cap_colloquial_03",
		Transcript: "Half dozen Indomie two-five thank you",
	}
	res3, err := engine.Validate(context.Background(), req3, "shop_1", "NGN", 0.75, "thank you", "stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if len(res3.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(res3.Lines))
	}
	l3 := res3.Lines[0].Transaction
	if l3.ProductID != "prd_indomie" {
		t.Errorf("expected product prd_indomie, got %q", l3.ProductID)
	}
	if l3.Quantity != 6 {
		t.Errorf("expected quantity 6 for half dozen, got %d", l3.Quantity)
	}
	if l3.Amount.Amount != 250000 { // 2,500 NGN = 250,000 kobo
		t.Errorf("expected amount 250000 kobo (2,500 NGN), got %d", l3.Amount.Amount)
	}
	if l3.Status != models.StatusConfirmed {
		t.Errorf("expected status confirmed, got %s (reasons: %v)", l3.Status, res3.Lines[0].ReviewReasons)
	}

	// 4. "Two painters of garri six thousand naira thank you" -> quantity 2, unit "painter", 6,000 NGN (600,000 kobo)
	req4 := validate.CaptureRequest{
		CaptureID:  "cap_colloquial_04",
		Transcript: "Two painters of garri six thousand naira thank you",
	}
	res4, err := engine.Validate(context.Background(), req4, "shop_1", "NGN", 0.75, "thank you", "stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if len(res4.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(res4.Lines))
	}
	l4 := res4.Lines[0].Transaction
	if l4.ProductID != "prd_garri" {
		t.Errorf("expected product prd_garri, got %q", l4.ProductID)
	}
	if l4.Quantity != 2 {
		t.Errorf("expected quantity 2, got %d", l4.Quantity)
	}
	if l4.Unit != "painter" {
		t.Errorf("expected unit painter, got %q", l4.Unit)
	}
	if l4.Amount.Amount != 600000 { // 6,000 NGN = 600,000 kobo
		t.Errorf("expected amount 600000 kobo (6,000 NGN), got %d", l4.Amount.Amount)
	}
	if l4.Status != models.StatusConfirmed {
		t.Errorf("expected status confirmed, got %s (reasons: %v)", l4.Status, res4.Lines[0].ReviewReasons)
	}
}

func TestColloquialParserVariants(t *testing.T) {
	tests := []struct {
		input       string
		wantQty     int
		wantUnit    models.Unit
		wantAmount  int64
		wantProduct string
	}{
		{
			input:       "2k Peak milk thank you",
			wantQty:     1,
			wantAmount:  200000,
			wantProduct: "Peak milk",
		},
		{
			input:       "1.5k Peak milk thank you",
			wantQty:     1,
			wantAmount:  150000,
			wantProduct: "Peak milk",
		},
		{
			input:       "two and half k Peak milk thank you",
			wantQty:     1,
			wantAmount:  250000,
			wantProduct: "Peak milk",
		},
		{
			input:       "k-twenty Milo thank you",
			wantQty:     1,
			wantAmount:  2000000,
			wantProduct: "Milo",
		},
		{
			input:       "k-hundred Milo thank you",
			wantQty:     1,
			wantAmount:  10000000,
			wantProduct: "Milo",
		},
		{
			input:       "one-five Indomie thank you",
			wantQty:     1,
			wantAmount:  150000,
			wantProduct: "Indomie",
		},
		{
			input:       "ten-five Milo thank you",
			wantQty:     1,
			wantAmount:  1050000,
			wantProduct: "Milo",
		},
		{
			input:       "two fifty Indomie thank you",
			wantQty:     1,
			wantAmount:  25000,
			wantProduct: "Indomie",
		},
		{
			input:       "seven fifty Indomie thank you",
			wantQty:     1,
			wantAmount:  75000,
			wantProduct: "Indomie",
		},
		{
			input:       "a dozen eggs two-five thank you",
			wantQty:     12,
			wantAmount:  250000,
			wantProduct: "eggs",
		},
		{
			input:       "a score of baskets fifty k thank you",
			wantQty:     20,
			wantAmount:  5000000,
			wantProduct: "baskets",
		},
		{
			input:       "a painter of garri three-five thank you",
			wantQty:     1,
			wantUnit:    models.Unit("painter"),
			wantAmount:  350000,
			wantProduct: "garri",
		},
		{
			input:       "three mudus of rice six thousand naira thank you",
			wantQty:     3,
			wantUnit:    models.Unit("mudu"),
			wantAmount:  600000,
			wantProduct: "rice",
		},
		{
			input:       "two dericas of beans one-five thank you",
			wantQty:     2,
			wantUnit:    models.Unit("derica"),
			wantAmount:  150000,
			wantProduct: "beans",
		},
		{
			input:       "four rubbers of palm oil k-twenty thank you",
			wantQty:     4,
			wantUnit:    models.Unit("rubber"),
			wantAmount:  2000000,
			wantProduct: "palm oil",
		},
	}

	for _, tc := range tests {
		anchors := validate.ParseSpokenSegment(tc.input, "NGN")
		if anchors.AmountMinor != tc.wantAmount {
			t.Errorf("ParseSpokenSegment(%q) amount = %d, want %d", tc.input, anchors.AmountMinor, tc.wantAmount)
		}
		if tc.wantQty > 0 && anchors.Quantity != tc.wantQty {
			t.Errorf("ParseSpokenSegment(%q) quantity = %d, want %d", tc.input, anchors.Quantity, tc.wantQty)
		}
		if tc.wantUnit != "" && anchors.Unit != tc.wantUnit {
			t.Errorf("ParseSpokenSegment(%q) unit = %q, want %q", tc.input, anchors.Unit, tc.wantUnit)
		}
		if tc.wantProduct != "" && anchors.ProductMention != tc.wantProduct {
			t.Errorf("ParseSpokenSegment(%q) product = %q, want %q", tc.input, anchors.ProductMention, tc.wantProduct)
		}
	}
}

// --- P4.2: stray closing phrases and chatter must not become review items ---

func TestP42StrayClosingPhraseIsNotHeldForReview(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	before := validate.AnchorlessSuppressedCount()

	// Roadmap P4.2 acceptance: ten stray "thank you"s create zero review items.
	// These are transcripts the parser resolves to no anchors at all, so they
	// used to become an "Unintelligible speech" review line.
	strays := []string{
		"Thank you.",
		"thank you",
		"Thank you",
		"thank you.",
		"THANK YOU.",
		"Thank you!",
		"thank you!",
		"Thank you..",
		" thank you",
		"Thank you. ",
	}
	for _, transcript := range strays {
		req := validate.CaptureRequest{CaptureID: "cap_stray", Transcript: transcript}
		res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
		if err != nil {
			t.Fatalf("Validate(%q) unexpected error: %v", transcript, err)
		}
		if !res.AnchorlessSuppressed {
			t.Errorf("Validate(%q): expected AnchorlessSuppressed true", transcript)
		}
		if len(res.Lines) != 0 {
			product := ""
			if len(res.Lines) > 0 {
				product = res.Lines[0].Transaction.ProductName
			}
			t.Errorf("Validate(%q): expected no lines, got %d (first line %q)", transcript, len(res.Lines), product)
		}
		if res.RequiresReview {
			t.Errorf("Validate(%q): expected no review requirement", transcript)
		}
	}

	if got := validate.AnchorlessSuppressedCount() - before; got != int64(len(strays)) {
		t.Errorf("AnchorlessSuppressedCount increased by %d, want %d", got, len(strays))
	}
}

func TestP42LegitimatePartialSaleIsStillValidated(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// A product mention with no quantity or amount is a partial-sale anchor, not
	// idle chatter. It must still be captured and held for review, not dropped.
	req := validate.CaptureRequest{CaptureID: "cap_partial", Transcript: "Milo"}
	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AnchorlessSuppressed {
		t.Fatalf("a product mention must not be treated as anchor-less chatter")
	}
	if len(res.Lines) != 1 {
		t.Fatalf("expected 1 line for the partial sale, got %d", len(res.Lines))
	}
	if !res.RequiresReview || res.Lines[0].Transaction.Status != models.StatusReviewRequired {
		t.Errorf("expected the partial sale to be held for review, got status %s", res.Lines[0].Transaction.Status)
	}
}

func TestP42SpokenCancellationWithoutAnchorsIsStillRecorded(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// "cancel that" carries no anchors, but it must still be recorded so the
	// spoken cancellation can void a prior entry.
	req := validate.CaptureRequest{CaptureID: "cap_cancel", Transcript: "cancel that"}
	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AnchorlessSuppressed {
		t.Fatalf("a spoken cancellation must not be suppressed")
	}
	if len(res.Lines) != 1 || res.Lines[0].Transaction.Status != models.StatusCancelled {
		t.Fatalf("expected one cancelled line, got %+v", res.Lines)
	}
}

func TestP42EmptyTranscriptKeepsReviewPlaceholder(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// A genuinely empty request is not closing-phrase chatter; keep the existing
	// review placeholder rather than silently discarding it.
	req := validate.CaptureRequest{CaptureID: "cap_empty", Transcript: ""}
	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AnchorlessSuppressed {
		t.Fatalf("an empty transcript must not be reported as anchor-less chatter")
	}
	if len(res.Lines) != 1 || res.Lines[0].Transaction.ProductName != "Unintelligible speech" {
		t.Fatalf("expected the unintelligible-speech placeholder, got %+v", res.Lines)
	}
}

// --- P5.2/P5.3: deterministic unit selection and cancellation precision ---

func TestP5UnitSelectionIsDeterministic(t *testing.T) {
	// A clause that names more than one unit used to resolve by Go map order, so
	// the same input could parse as "carton" or "pack" from run to run.
	const mixed = "two cartons and three packs of Milo"

	first := validate.ParseSpokenSegment(mixed, "NGN")
	if !first.UnitFound {
		t.Fatalf("expected a unit to be found in %q", mixed)
	}
	if first.Unit != models.Unit("carton") {
		t.Fatalf("expected the earliest unit (carton), got %q", first.Unit)
	}
	for i := 0; i < 200; i++ {
		got := validate.ParseSpokenSegment(mixed, "NGN")
		if got.Unit != first.Unit {
			t.Fatalf("unit selection varied on run %d: %q then %q", i, got.Unit, first.Unit)
		}
	}
}

func TestP5UnitSelectionFollowsReadingOrder(t *testing.T) {
	if got := validate.ParseSpokenSegment("three packs and two cartons", "NGN").Unit; got != models.Unit("pack") {
		t.Errorf("expected the first unit pack, got %q", got)
	}
	if got := validate.ParseSpokenSegment("three cartons and two packs", "NGN").Unit; got != models.Unit("carton") {
		t.Errorf("expected the first unit carton, got %q", got)
	}
}

func TestP5OrdinaryInstructionsDoNotCancel(t *testing.T) {
	// P5.3: "drop that" is an ordinary instruction when it carries an object, so
	// it must not cancel a sale. The audit's example is reproduced verbatim.
	ordinary := []string{
		"Drop that bag there, two Milo four thousand naira thank you",
		"Drop that bag on the counter",
		"Please drop that crate by the door",
	}
	for _, transcript := range ordinary {
		if phrase, ok := validate.DetectCancellation(transcript); ok {
			t.Errorf("DetectCancellation(%q) = %q, want no cancellation", transcript, phrase)
		}
	}

	// Genuine cancellations still cancel.
	genuine := []string{
		"cancel that",
		"never mind",
		"customer cancelled the purchase",
		"Two milo 4000 naira wait customer cancelled",
		"drop that",
		"Never mind, drop that",
	}
	for _, transcript := range genuine {
		if _, ok := validate.DetectCancellation(transcript); !ok {
			t.Errorf("DetectCancellation(%q) = false, want a cancellation", transcript)
		}
	}
}

func TestP5InstructionDoesNotVoidASale(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	req := validate.CaptureRequest{
		CaptureID:  "cap_p5_instruction",
		Transcript: "Drop that bag there, two Milo four thousand naira thank you",
	}
	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	matchedMilo := false
	for _, line := range res.Lines {
		if line.Transaction.Status == models.StatusCancelled {
			t.Fatalf("an ordinary instruction cancelled the sale: %+v", line)
		}
		if line.Transaction.ProductID == "prd_milo" {
			matchedMilo = true
		}
	}
	if !matchedMilo {
		t.Fatalf("expected the Milo sale to survive the instruction, got lines %+v", res.Lines)
	}
}

// --- P5.1: number words inside brand names ---

func p51Catalogue() []models.Product {
	return []models.Product{
		{ID: "prd_five_alive", ShopID: "shop_1", Name: "Five Alive", SKU: "FA-500",
			Price: models.Money{Amount: 100000, Currency: "NGN"}, Quantity: 40, BaseUnit: "pack"},
		{ID: "prd_seven_up", ShopID: "shop_1", Name: "Seven Up", SKU: "7UP-50",
			Aliases: []string{"7Up"}, Price: models.Money{Amount: 100000, Currency: "NGN"}, Quantity: 40, BaseUnit: "bottle"},
		{ID: "prd_three_crowns", ShopID: "shop_1", Name: "Three Crowns", SKU: "TC-400",
			Price: models.Money{Amount: 100000, Currency: "NGN"}, Quantity: 30, BaseUnit: "tin"},
		{ID: "prd_milo", ShopID: "shop_1", Name: "Milo 500g", SKU: "MILO-500",
			Aliases:  []string{"Milo", "Nestle Milo", "Milo bottle"},
			Price:    models.Money{Amount: 200000, Currency: "NGN"},
			Quantity: 20, BaseUnit: "pack",
			Packaging: []models.PackagingRelation{{From: "carton", To: "pack", Factor: 12}}},
	}
}

func TestP51NumberBearingBrandNamesParse(t *testing.T) {
	names := []string{"Five Alive", "Seven Up", "7Up", "Three Crowns", "Milo 500g", "Milo bottle", "Milo"}

	cases := []struct {
		input    string
		product  string
		quantity int
		amount   int64
	}{
		{"Two Five Alive, one thousand naira", "Five Alive", 2, 100000},
		{"Two Seven Up, one thousand naira", "Seven Up", 2, 100000},
		{"Two 7Up, one thousand naira", "7Up", 2, 100000},
		{"Two Three Crowns, one thousand naira", "Three Crowns", 2, 100000},
	}
	for _, tc := range cases {
		anchors := validate.ParseSpokenSegmentWithProducts(tc.input, "NGN", names)
		if anchors.ProductMention != tc.product {
			t.Errorf("product for %q = %q, want %q", tc.input, anchors.ProductMention, tc.product)
		}
		if anchors.Quantity != tc.quantity || !anchors.QuantityFound {
			t.Errorf("quantity for %q = %d (found=%v), want %d", tc.input, anchors.Quantity, anchors.QuantityFound, tc.quantity)
		}
		if anchors.AmountMinor != tc.amount {
			t.Errorf("amount for %q = %d, want %d", tc.input, anchors.AmountMinor, tc.amount)
		}
	}
}

func TestP51BrandNameContainingUnitKeepsTheUnit(t *testing.T) {
	anchors := validate.ParseSpokenSegmentWithProducts("two Milo bottle five hundred naira", "NGN", []string{"Milo bottle"})
	if anchors.ProductMention != "Milo bottle" {
		t.Fatalf("product = %q, want %q", anchors.ProductMention, "Milo bottle")
	}
	if !anchors.UnitFound || anchors.Unit != models.Unit("bottle") {
		t.Fatalf("unit = %q (found=%v), want bottle", anchors.Unit, anchors.UnitFound)
	}
	if anchors.Quantity != 2 {
		t.Fatalf("quantity = %d, want 2", anchors.Quantity)
	}
	if anchors.AmountMinor != 50000 {
		t.Fatalf("amount = %d, want 50000", anchors.AmountMinor)
	}
}

func TestP51EngineMatchesNumberBearingBrands(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := p51Catalogue()

	cases := []struct {
		transcript string
		productID  string
	}{
		{"Two Five Alive, one thousand naira thank you", "prd_five_alive"},
		{"Two Seven Up, one thousand naira thank you", "prd_seven_up"},
		{"Two 7Up, one thousand naira thank you", "prd_seven_up"},
		{"Two Three Crowns, one thousand naira thank you", "prd_three_crowns"},
	}
	for _, tc := range cases {
		req := validate.CaptureRequest{CaptureID: "cap_p51", Transcript: tc.transcript}
		res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.transcript, err)
		}
		if len(res.Lines) != 1 {
			t.Fatalf("expected one line for %q, got %d (%+v)", tc.transcript, len(res.Lines), res.Lines)
		}
		line := res.Lines[0]
		if line.Transaction.ProductID != tc.productID {
			t.Errorf("product for %q = %q, want %q", tc.transcript, line.Transaction.ProductID, tc.productID)
		}
		if line.Transaction.Quantity != 2 {
			t.Errorf("quantity for %q = %d, want 2", tc.transcript, line.Transaction.Quantity)
		}
		if line.Transaction.Amount.Amount != 100000 {
			t.Errorf("amount for %q = %d, want 100000", tc.transcript, line.Transaction.Amount.Amount)
		}
	}
}

func TestP51PlainProductsStillParse(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := p51Catalogue()

	req := validate.CaptureRequest{CaptureID: "cap_p51_plain", Transcript: "two Milo 4000 naira thank you"}
	res, err := engine.Validate(context.Background(), req, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Lines) != 1 {
		t.Fatalf("expected one line, got %d (%+v)", len(res.Lines), res.Lines)
	}
	line := res.Lines[0]
	if line.Transaction.ProductID != "prd_milo" || line.Transaction.Quantity != 2 || line.Transaction.Amount.Amount != 400000 {
		t.Fatalf("plain Milo sale parsed as %+v", line.Transaction)
	}
}

// P5.5: the parser must distinguish a quantity it heard from one it defaulted.
func TestParseSpokenSegment_QuantityInferredFlag(t *testing.T) {
	cases := []struct {
		input    string
		quantity int
		found    bool
		inferred bool
	}{
		{"Milo, four thousand naira", 1, true, true},
		{"Two Milo, four thousand naira", 2, true, false},
		{"Milo", 0, false, false},
	}
	for _, tc := range cases {
		anchors := validate.ParseSpokenSegment(tc.input, "NGN")
		if anchors.Quantity != tc.quantity || anchors.QuantityFound != tc.found || anchors.QuantityInferred != tc.inferred {
			t.Errorf("ParseSpokenSegment(%q) = qty %d (found=%v, inferred=%v), want qty %d (found=%v, inferred=%v)",
				tc.input, anchors.Quantity, anchors.QuantityFound, anchors.QuantityInferred,
				tc.quantity, tc.found, tc.inferred)
		}
	}
}

// P5.5: an inferred quantity is a guess, so it must score below a spoken one and
// hold the movement for a human instead of auto-confirming stock from a number
// nobody said (README.md#history, historical audit finding 5).
func TestValidate_InferredQuantityIsHeldForReview(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	inferred, err := engine.Validate(context.Background(), validate.CaptureRequest{
		CaptureID:  "cap_inferred_qty",
		Transcript: "Milo, two thousand naira. Thank you.",
	}, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inferred.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(inferred.Lines))
	}
	line := inferred.Lines[0].Transaction
	if line.Quantity != 1 {
		t.Errorf("expected the defaulted quantity 1, got %d", line.Quantity)
	}
	if line.Status != models.StatusReviewRequired {
		t.Errorf("expected an inferred quantity to be held for review, got %s (reasons: %v)",
			line.Status, inferred.Lines[0].ReviewReasons)
	}
	if q := line.Confidence.Quantity; q == nil || *q >= 0.75 {
		t.Errorf("expected an inferred quantity confidence below the review threshold, got %v", q)
	}

	spoken, err := engine.Validate(context.Background(), validate.CaptureRequest{
		CaptureID:  "cap_spoken_qty",
		Transcript: "One Milo, two thousand naira. Thank you.",
	}, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spoken.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(spoken.Lines))
	}
	spokenLine := spoken.Lines[0].Transaction
	if spokenLine.Status != models.StatusConfirmed {
		t.Errorf("expected an explicitly spoken quantity to confirm, got %s (reasons: %v)",
			spokenLine.Status, spoken.Lines[0].ReviewReasons)
	}
	if q := spokenLine.Confidence.Quantity; q == nil || *q != 0.98 {
		t.Errorf("expected a spoken quantity confidence of 0.98, got %v", q)
	}

	// A caller cannot lower its own confidence: extracted_lines is client JSON
	// and the inferred flag is not settable through it.
	client, err := engine.Validate(context.Background(), validate.CaptureRequest{
		CaptureID: "cap_client_qty",
		ExtractedLines: []validate.CaptureLineInput{{
			ProductName: "Milo",
			Quantity:    1,
			Unit:        models.Unit("pack"),
			Amount:      &models.Money{Amount: 200000, Currency: "NGN"},
		}},
	}, "shop_1", "NGN", 0.75, "Thank you", "Stock received", catalogue)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q := client.Lines[0].Transaction.Confidence.Quantity; q == nil || *q != 0.98 {
		t.Errorf("expected a client-supplied quantity to stay at 0.98, got %v", q)
	}
}
