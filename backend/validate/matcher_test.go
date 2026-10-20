package validate_test

import (
	"context"
	"testing"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/validate"
)

func TestDoubleMetaphone(t *testing.T) {
	pairs := []struct {
		wordA string
		wordB string
	}{
		{"peek", "peak"},
		{"milo", "mylo"},
		{"coke", "koke"},
		{"knorr", "nor"},
		{"fanta", "phanta"},
		{"sugar", "shugar"},
		{"maltina", "malteena"},
		{"pepsi", "pepsee"},
		{"cereal", "sereal"},
	}

	for _, pair := range pairs {
		p1, s1 := validate.DoubleMetaphone(pair.wordA)
		p2, s2 := validate.DoubleMetaphone(pair.wordB)

		match := (p1 != "" && (p1 == p2 || (s2 != "" && p1 == s2))) ||
			(s1 != "" && (s1 == p2 || (s2 != "" && s1 == s2)))

		if !match {
			t.Errorf("expected DoubleMetaphone match for %q (%s, %s) vs %q (%s, %s)",
				pair.wordA, p1, s1, pair.wordB, p2, s2)
		}
	}
}

func TestMatchProduct_Phonetic(t *testing.T) {
	catalogue := []models.Product{
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
		{
			ID:       "prd_milo",
			ShopID:   "shop_1",
			Name:     "Milo 500g",
			SKU:      "MILO-500",
			Aliases:  []string{"Milo", "Nestle Milo"},
			Price:    models.Money{Amount: 200000, Currency: "NGN"},
			Quantity: 20,
			BaseUnit: "pack",
		},
		{
			ID:       "prd_coke",
			ShopID:   "shop_1",
			Name:     "Coca-Cola 50cl",
			SKU:      "COKE-50",
			Aliases:  []string{"Coke", "Coca Cola", "Coke bottle"},
			Price:    models.Money{Amount: 250000, Currency: "NGN"},
			Quantity: 30,
			BaseUnit: "bottle",
		},
	}

	// 1. Spoken phonetic mishearing "peek" -> "Peak Milk 400g"
	res := validate.MatchProduct("peek", catalogue)
	if res.Product == nil || res.Product.ID != "prd_peak" {
		t.Fatalf("expected 'peek' to match prd_peak, got %+v", res)
	}
	if res.MatchedBy != "phonetic" {
		t.Errorf("expected MatchedBy 'phonetic', got %q", res.MatchedBy)
	}
	if res.Confidence < 0.88 || res.Confidence > 0.95 {
		t.Errorf("expected confidence between 0.88 and 0.95, got %f", res.Confidence)
	}
	if res.Ambiguous {
		t.Errorf("expected ambiguous to be false for unique match, got true")
	}

	// 2. Multi-word phonetic mishearing "peek milk" -> "Peak Milk 400g"
	res = validate.MatchProduct("peek milk", catalogue)
	if res.Product == nil || res.Product.ID != "prd_peak" {
		t.Fatalf("expected 'peek milk' to match prd_peak, got %+v", res)
	}
	if res.MatchedBy != "phonetic" {
		t.Errorf("expected MatchedBy 'phonetic', got %q", res.MatchedBy)
	}
	if res.Confidence < 0.90 {
		t.Errorf("expected confidence >= 0.90 for multi-word phonetic match, got %f", res.Confidence)
	}

	// 3. Spoken "mylo" -> "Milo 500g"
	res = validate.MatchProduct("mylo", catalogue)
	if res.Product == nil || res.Product.ID != "prd_milo" {
		t.Fatalf("expected 'mylo' to match prd_milo, got %+v", res)
	}
	if res.MatchedBy != "phonetic" {
		t.Errorf("expected MatchedBy 'phonetic', got %q", res.MatchedBy)
	}
	if res.Confidence < 0.88 {
		t.Errorf("expected confidence >= 0.88, got %f", res.Confidence)
	}

	// 4. Spoken "koke" -> "Coca-Cola 50cl" via alias "Coke"
	res = validate.MatchProduct("koke", catalogue)
	if res.Product == nil || res.Product.ID != "prd_coke" {
		t.Fatalf("expected 'koke' to match prd_coke, got %+v", res)
	}
	if res.MatchedBy != "phonetic" {
		t.Errorf("expected MatchedBy 'phonetic', got %q", res.MatchedBy)
	}
	if res.Confidence < 0.88 {
		t.Errorf("expected confidence >= 0.88, got %f", res.Confidence)
	}
}

func TestMatchProduct_PhoneticAmbiguity(t *testing.T) {
	// Two sizes of Peak Milk in catalogue
	catalogue := []models.Product{
		{
			ID:       "prd_peak_400",
			ShopID:   "shop_1",
			Name:     "Peak Milk 400g",
			SKU:      "PEAK-400",
			Aliases:  []string{"Peak", "Peak 400"},
			Price:    models.Money{Amount: 350000, Currency: "NGN"},
			Quantity: 15,
			BaseUnit: "tin",
		},
		{
			ID:       "prd_peak_900",
			ShopID:   "shop_1",
			Name:     "Peak Milk 900g",
			SKU:      "PEAK-900",
			Aliases:  []string{"Peak", "Peak 900"},
			Price:    models.Money{Amount: 750000, Currency: "NGN"},
			Quantity: 10,
			BaseUnit: "tin",
		},
	}

	// Spoken "peek" matches both Peak products equally -> Ambiguous match (0.50)
	res := validate.MatchProduct("peek", catalogue)
	if res.Product == nil {
		t.Fatalf("expected product to be populated on ambiguous match, got nil")
	}
	if !res.Ambiguous {
		t.Errorf("expected Ambiguous true on phonetic collision, got false")
	}
	if res.Confidence != 0.50 {
		t.Errorf("expected Confidence 0.50 on ambiguous collision, got %f", res.Confidence)
	}
	if res.MatchedBy != "phonetic" {
		t.Errorf("expected MatchedBy 'phonetic', got %q", res.MatchedBy)
	}

	// Spoken "peek 400" disambiguates cleanly to Peak 400
	res = validate.MatchProduct("peek 400", catalogue)
	if res.Product == nil || res.Product.ID != "prd_peak_400" {
		t.Fatalf("expected disambiguated match for prd_peak_400, got %+v", res)
	}
	if res.Ambiguous {
		t.Errorf("expected Ambiguous false on disambiguated mention, got true")
	}
	if res.Confidence < 0.88 {
		t.Errorf("expected Confidence >= 0.88, got %f", res.Confidence)
	}
}

func TestMatchCustomer_Phonetic(t *testing.T) {
	customers := []models.Customer{
		{
			ID:     "cust_emeka",
			ShopID: "shop_1",
			Name:   "Emeka Okafor",
			Phone:  "+2348011112222",
		},
		{
			ID:     "cust_fatima",
			ShopID: "shop_1",
			Name:   "Fatima Bello",
			Phone:  "+2348033334444",
		},
	}

	// Spoken "Emecca" matches "Emeka Okafor" phonetically
	res := validate.MatchCustomer("Emecca", customers)
	if res.Customer == nil || res.Customer.ID != "cust_emeka" {
		t.Fatalf("expected phonetic match for cust_emeka, got %+v", res)
	}
	if res.MatchedBy != "phonetic" {
		t.Errorf("expected MatchedBy 'phonetic', got %q", res.MatchedBy)
	}
	if res.Confidence < 0.88 {
		t.Errorf("expected confidence >= 0.88, got %f", res.Confidence)
	}
}

func TestP54PhoneticProductMatchingInEngine(t *testing.T) {
	engine := validate.NewEngine()
	catalogue := sampleCatalogue()

	// Spoken mention with phonetic mishearing "peek" instead of "Peak"
	req := validate.CaptureRequest{
		CaptureID:  "cap_p54_phonetic",
		Transcript: "Two peek, seven thousand naira. Thank you.",
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
	if line.Transaction.ProductID != "prd_peak" {
		t.Errorf("expected product prd_peak, got %s", line.Transaction.ProductID)
	}
	if line.Transaction.Quantity != 2 {
		t.Errorf("expected quantity 2, got %d", line.Transaction.Quantity)
	}
	if line.Transaction.Amount.Amount != 700000 {
		t.Errorf("expected amount 700000, got %d", line.Transaction.Amount.Amount)
	}
	if line.Transaction.Status != models.StatusConfirmed {
		t.Errorf("expected status confirmed, got %s (reasons: %v)", line.Transaction.Status, line.ReviewReasons)
	}
	if res.RequiresReview {
		t.Errorf("expected RequiresReview false for clean phonetic match, got true")
	}
	if line.MatchedBy != "phonetic" {
		t.Errorf("expected MatchedBy 'phonetic', got %q", line.MatchedBy)
	}
}
