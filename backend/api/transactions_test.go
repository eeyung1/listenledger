package api

import (
	"github.com/eeyung1/listenledger-backend/models"
	"testing"
)

func TestConfidenceCannotBypassReview(t *testing.T) {
	high, low := 0.99, 0.2
	for name, c := range map[string]*models.Confidence{"missing": nil, "partial": {Overall: high, Product: &high}, "weak": {Overall: high, Product: &low, Quantity: &high, Amount: &high}, "zero overall": {Overall: 0, Product: &high, Quantity: &high, Amount: &high}} {
		if deriveStatus(c) != models.StatusReviewRequired {
			t.Fatal(name)
		}
	}
	if deriveStatus(&models.Confidence{Overall: high, Product: &high, Quantity: &high, Amount: &high}) != models.StatusConfirmed {
		t.Fatal("strong anchors held")
	}
	b := createTransactionRequest{Type: models.TypeSale, ProductName: "Milo", Quantity: 1, Unit: models.UnitPack, Amount: models.Money{Currency: "NGN", Amount: 100}, Status: models.StatusConfirmed}
	if _, e := b.transaction("a"); e == nil {
		t.Fatal("caller forced confirmation")
	}
}
