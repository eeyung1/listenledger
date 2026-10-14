package eval

import (
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/validate"
)

// Scenario represents a realistic counter interaction in a retail shop trial.
type Scenario struct {
	ID                 string
	Transcript         string
	ClosingPhrase      string
	Lines              []validate.CaptureLineInput
	ExpectedType       models.TransactionType
	ExpectedProductID  string
	ExpectedQty        int
	ExpectedUnit       models.Unit
	ExpectedAmountKobo int64
	ShouldCapture      bool
	ShouldConfirm      bool
	IsOfflineBurst     bool
	IsDuplicateRetry   bool
	IsDisputed         bool
}

// TargetGates represents the pre-agreed objective gates before the trial.
type TargetGates struct {
	MinFullTransactionAccuracy float64 // e.g. 90.0%
	MinSKUAccuracy             float64 // e.g. 95.0%
	MinQuantityAccuracy        float64 // e.g. 95.0%
	MinUnitAccuracy            float64 // e.g. 95.0%
	MinAmountAccuracy          float64 // e.g. 95.0%
	MaxFalseCaptureRate        float64 // e.g. 3.0%
	MaxMissedTransactionRate   float64 // e.g. 5.0%
	MaxCorrectionRate          float64 // e.g. 10.0%
	MinProtocolComplianceRate  float64 // e.g. 85.0%
	MaxP95LatencyMs            float64 // e.g. 2000ms
}

// DefaultTargetGates defines the strict production acceptance thresholds.
var DefaultTargetGates = TargetGates{
	MinFullTransactionAccuracy: 90.0,
	MinSKUAccuracy:             95.0,
	MinQuantityAccuracy:        95.0,
	MinUnitAccuracy:            95.0,
	MinAmountAccuracy:          95.0,
	MaxFalseCaptureRate:        3.0,
	MaxMissedTransactionRate:   5.0,
	MaxCorrectionRate:          10.0,
	MinProtocolComplianceRate:  85.0,
	MaxP95LatencyMs:            1500.0,
}

// InitialCatalogue returns the pilot shop's baseline product set.
func InitialCatalogue() []models.Product {
	return []models.Product{
		{
			ID:                "prd_milo_500",
			Name:              "Milo 500g",
			SKU:               "MILO-500",
			Aliases:           []string{"Milo", "Big Milo", "Nestle Milo"},
			Price:             models.Money{Amount: 250000, Currency: "NGN"},
			Quantity:          50,
			BaseUnit:          models.UnitPack,
			LowStockThreshold: 10,
			Packaging: []models.PackagingRelation{
				{From: models.UnitCarton, To: models.UnitPack, Factor: 12},
			},
		},
		{
			ID:                "prd_peak_400",
			Name:              "Peak Milk 400g",
			SKU:               "PEAK-400",
			Aliases:           []string{"Peak", "Big Peak", "Peak Milk"},
			Price:             models.Money{Amount: 350000, Currency: "NGN"},
			Quantity:          40,
			BaseUnit:          models.UnitPiece,
			LowStockThreshold: 5,
		},
		{
			ID:                "prd_indomie_70",
			Name:              "Indomie Chicken 70g",
			SKU:               "INDOMIE-70",
			Aliases:           []string{"Indomie", "Noodles", "Chicken Noodles"},
			Price:             models.Money{Amount: 15000, Currency: "NGN"},
			Quantity:          120,
			BaseUnit:          models.UnitPack,
			LowStockThreshold: 20,
			Packaging: []models.PackagingRelation{
				{From: models.UnitCarton, To: models.UnitPack, Factor: 40},
			},
		},
		{
			ID:                "prd_coke_50",
			Name:              "Coca-Cola 50cl",
			SKU:               "COKE-50",
			Aliases:           []string{"Coke", "Coca Cola", "Mineral"},
			Price:             models.Money{Amount: 30000, Currency: "NGN"},
			Quantity:          60,
			BaseUnit:          models.UnitBottle,
			LowStockThreshold: 12,
			Packaging: []models.PackagingRelation{
				{From: models.UnitCrate, To: models.UnitBottle, Factor: 24},
			},
		},
		{
			ID:                "prd_sugar_st",
			Name:              "St Louis Sugar",
			SKU:               "SUGAR-ST",
			Aliases:           []string{"Sugar", "St Louis", "Cube Sugar"},
			Price:             models.Money{Amount: 120000, Currency: "NGN"},
			Quantity:          30,
			BaseUnit:          models.UnitPiece,
			LowStockThreshold: 5,
		},
	}
}

func makeConfidence(score float64) *models.Confidence {
	return &models.Confidence{
		Overall:  score,
		Product:  &score,
		Quantity: &score,
	}
}

// BenchmarkScenarios returns 20 controlled counter interaction scenarios.
func BenchmarkScenarios() []Scenario {
	confHigh := makeConfidence(0.96)
	confLow := makeConfidence(0.40)
	confDispute := makeConfidence(0.25)

	return []Scenario{
		{
			ID:            "trial_01",
			Transcript:    "Two packs of Milo. That will be five thousand naira. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Milo", Quantity: 2, Unit: models.UnitPack, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_milo_500",
			ExpectedQty:        2,
			ExpectedUnit:       models.UnitPack,
			ExpectedAmountKobo: 500000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_02",
			Transcript:    "One Peak Milk. Three thousand five hundred. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Peak Milk", Quantity: 1, Unit: models.UnitPiece, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_peak_400",
			ExpectedQty:        1,
			ExpectedUnit:       models.UnitPiece,
			ExpectedAmountKobo: 350000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_03",
			Transcript:    "Stock received: five cartons of Milo from wholesaler, stock received.",
			ClosingPhrase: "Stock received",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Milo", Quantity: 5, Unit: models.UnitCarton, Confidence: confHigh},
			},
			ExpectedType:       models.TypeRestock,
			ExpectedProductID:  "prd_milo_500",
			ExpectedQty:        5,
			ExpectedUnit:       models.UnitCarton,
			ExpectedAmountKobo: 0,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_04",
			Transcript:    "Three bottles of chilled Coke, nine hundred naira. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Coke", Quantity: 3, Unit: models.UnitBottle, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_coke_50",
			ExpectedQty:        3,
			ExpectedUnit:       models.UnitBottle,
			ExpectedAmountKobo: 90000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_05",
			Transcript:    "Five Indomie noodles, seven hundred and fifty naira. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Indomie", Quantity: 5, Unit: models.UnitPack, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_indomie_70",
			ExpectedQty:        5,
			ExpectedUnit:       models.UnitPack,
			ExpectedAmountKobo: 75000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_06_ambiguous",
			Transcript:    "Give me two small powders please. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "small powders", Quantity: 2, Unit: models.UnitPiece, Confidence: confLow},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "",
			ExpectedQty:        2,
			ExpectedUnit:       models.UnitPiece,
			ExpectedAmountKobo: 0,
			ShouldCapture:      true,
			ShouldConfirm:      false, // Weak confidence -> ReviewRequired
		},
		{
			ID:            "trial_07_chatter",
			Transcript:    "Good morning neighbor, hope the family is well? Yes everything is fine.",
			ClosingPhrase: "", // No closing phrase
			Lines:         nil,
			ShouldCapture: false, // Discarded: no false capture
		},
		{
			ID:            "trial_08",
			Transcript:    "Stock received: two cartons of Indomie chicken, stock received.",
			ClosingPhrase: "Stock received",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Indomie", Quantity: 2, Unit: models.UnitCarton, Confidence: confHigh},
			},
			ExpectedType:       models.TypeRestock,
			ExpectedProductID:  "prd_indomie_70",
			ExpectedQty:        2,
			ExpectedUnit:       models.UnitCarton,
			ExpectedAmountKobo: 0,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_09",
			Transcript:    "Four tins of Peak milk, fourteen thousand naira. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Peak", Quantity: 4, Unit: models.UnitPiece, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_peak_400",
			ExpectedQty:        4,
			ExpectedUnit:       models.UnitPiece,
			ExpectedAmountKobo: 1400000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_10_chatter",
			Transcript:    "Please lend me that pen for a minute, I want to sign this receipt.",
			ClosingPhrase: "",
			Lines:         nil,
			ShouldCapture: false, // Discarded
		},
		{
			ID:            "trial_11",
			Transcript:    "Stock received: one crate of Coca Cola bottles, stock received.",
			ClosingPhrase: "Stock received",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Coca Cola", Quantity: 1, Unit: models.UnitCrate, Confidence: confHigh},
			},
			ExpectedType:       models.TypeRestock,
			ExpectedProductID:  "prd_coke_50",
			ExpectedQty:        1,
			ExpectedUnit:       models.UnitCrate,
			ExpectedAmountKobo: 0,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_12",
			Transcript:    "Two packs of Milo, five thousand. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Milo", Quantity: 2, Unit: models.UnitPack, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_milo_500",
			ExpectedQty:        2,
			ExpectedUnit:       models.UnitPack,
			ExpectedAmountKobo: 500000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_13",
			Transcript:    "One box of St Louis sugar, one thousand two hundred. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "St Louis", Quantity: 1, Unit: models.UnitPiece, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_sugar_st",
			ExpectedQty:        1,
			ExpectedUnit:       models.UnitPiece,
			ExpectedAmountKobo: 120000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_14",
			Transcript:    "Ten packs of Indomie noodles, one thousand five hundred naira. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Indomie", Quantity: 10, Unit: models.UnitPack, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_indomie_70",
			ExpectedQty:        10,
			ExpectedUnit:       models.UnitPack,
			ExpectedAmountKobo: 150000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_15_disputed",
			Transcript:    "Did you watch the football game last night? Yes Arsenal won. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "football game", Quantity: 1, Unit: models.UnitPiece, Confidence: confDispute},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "",
			ExpectedQty:        1,
			ExpectedUnit:       models.UnitPiece,
			ExpectedAmountKobo: 0,
			ShouldCapture:      true,
			ShouldConfirm:      false,
			IsDisputed:         true, // Owner will reject in review
		},
		{
			ID:             "trial_16_offline_1",
			Transcript:     "One bottle of Coke, three hundred naira. Thank you.",
			ClosingPhrase:  "Thank you",
			IsOfflineBurst: true,
			Lines: []validate.CaptureLineInput{
				{ProductName: "Coke", Quantity: 1, Unit: models.UnitBottle, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_coke_50",
			ExpectedQty:        1,
			ExpectedUnit:       models.UnitBottle,
			ExpectedAmountKobo: 30000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:             "trial_17_offline_2",
			Transcript:     "Two boxes of sugar, two thousand four hundred. Thank you.",
			ClosingPhrase:  "Thank you",
			IsOfflineBurst: true,
			Lines: []validate.CaptureLineInput{
				{ProductName: "sugar", Quantity: 2, Unit: models.UnitPiece, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_sugar_st",
			ExpectedQty:        2,
			ExpectedUnit:       models.UnitPiece,
			ExpectedAmountKobo: 240000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:             "trial_18_offline_3",
			Transcript:     "Three packs of Indomie, four hundred and fifty naira. Thank you.",
			ClosingPhrase:  "Thank you",
			IsOfflineBurst: true,
			Lines: []validate.CaptureLineInput{
				{ProductName: "Indomie", Quantity: 3, Unit: models.UnitPack, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_indomie_70",
			ExpectedQty:        3,
			ExpectedUnit:       models.UnitPack,
			ExpectedAmountKobo: 45000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:               "trial_19_duplicate",
			Transcript:       "Two packs of Milo. That will be five thousand naira. Thank you.",
			ClosingPhrase:    "Thank you",
			IsDuplicateRetry: true, // Replay of trial_01 with identical idempotency key
			Lines: []validate.CaptureLineInput{
				{ProductName: "Milo", Quantity: 2, Unit: models.UnitPack, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_milo_500",
			ExpectedQty:        2,
			ExpectedUnit:       models.UnitPack,
			ExpectedAmountKobo: 500000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
		{
			ID:            "trial_20",
			Transcript:    "One tin of Peak milk, three thousand five hundred naira. Thank you.",
			ClosingPhrase: "Thank you",
			Lines: []validate.CaptureLineInput{
				{ProductName: "Peak", Quantity: 1, Unit: models.UnitPiece, Confidence: confHigh},
			},
			ExpectedType:       models.TypeSale,
			ExpectedProductID:  "prd_peak_400",
			ExpectedQty:        1,
			ExpectedUnit:       models.UnitPiece,
			ExpectedAmountKobo: 350000,
			ShouldCapture:      true,
			ShouldConfirm:      true,
		},
	}
}

// TrialMetrics holds computed performance indicators from running the benchmark.
type TrialMetrics struct {
	TotalInteractions        int
	LegitimateCommercial     int
	NonCommercialCount       int
	CapturedCount            int
	MissedCount              int
	FalseCaptureCount        int
	ConfirmedCount           int
	ReviewCount              int
	OwnerCorrections         int
	OwnerRejections          int
	DuplicateReplays         int
	FullTransactionAcc       float64
	SKUAccuracy              float64
	QuantityAccuracy         float64
	UnitAccuracy             float64
	AmountAccuracy           float64
	MissedTransactionRate    float64
	FalseCaptureRate         float64
	CorrectionRate           float64
	ProtocolComplianceRate   float64
	StockDiscrepancyCount    int
	RevenueDiscrepancyAmount int64
	P50LatencyMs             float64
	P90LatencyMs             float64
	P95LatencyMs             float64
	P99LatencyMs             float64
	ExecutionDuration        time.Duration
}
