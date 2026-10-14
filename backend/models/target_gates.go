package models

import (
	"errors"
	"fmt"
	"time"
)

// TargetGates represents the pre-agreed objective pilot release gates agreed with the shop owner.
type TargetGates struct {
	MinFullTransactionAccuracy float64    `json:"min_full_transaction_accuracy"` // e.g. 90.0%
	MinSKUAccuracy             float64    `json:"min_sku_accuracy"`              // e.g. 95.0%
	MinQuantityAccuracy        float64    `json:"min_quantity_accuracy"`         // e.g. 95.0%
	MinUnitAccuracy            float64    `json:"min_unit_accuracy"`             // e.g. 95.0%
	MinAmountAccuracy          float64    `json:"min_amount_accuracy"`           // e.g. 95.0%
	MaxFalseCaptureRate        float64    `json:"max_false_capture_rate"`        // e.g. 3.0%
	MaxMissedTransactionRate   float64    `json:"max_missed_transaction_rate"`   // e.g. 5.0%
	MaxCorrectionRate          float64    `json:"max_correction_rate"`           // e.g. 10.0%
	MinProtocolComplianceRate  float64    `json:"min_protocol_compliance_rate"`  // e.g. 85.0%
	MaxP95LatencyMs            float64    `json:"max_p95_latency_ms"`            // e.g. 1500.0 ms
	AgreedAt                   *time.Time `json:"agreed_at,omitempty"`
	AgreedBy                   string     `json:"agreed_by,omitempty"`
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

// Validate checks that all target gates fall within realistic percentages and latencies.
func (g TargetGates) Validate() error {
	checkRate := func(name string, val float64) error {
		if val < 0.0 || val > 100.0 {
			return fmt.Errorf("%s must be between 0.0%% and 100.0%% (got %.2f)", name, val)
		}
		return nil
	}

	if err := checkRate("min_full_transaction_accuracy", g.MinFullTransactionAccuracy); err != nil {
		return err
	}
	if err := checkRate("min_sku_accuracy", g.MinSKUAccuracy); err != nil {
		return err
	}
	if err := checkRate("min_quantity_accuracy", g.MinQuantityAccuracy); err != nil {
		return err
	}
	if err := checkRate("min_unit_accuracy", g.MinUnitAccuracy); err != nil {
		return err
	}
	if err := checkRate("min_amount_accuracy", g.MinAmountAccuracy); err != nil {
		return err
	}
	if err := checkRate("max_false_capture_rate", g.MaxFalseCaptureRate); err != nil {
		return err
	}
	if err := checkRate("max_missed_transaction_rate", g.MaxMissedTransactionRate); err != nil {
		return err
	}
	if err := checkRate("max_correction_rate", g.MaxCorrectionRate); err != nil {
		return err
	}
	if err := checkRate("min_protocol_compliance_rate", g.MinProtocolComplianceRate); err != nil {
		return err
	}

	if g.MaxP95LatencyMs <= 0 || g.MaxP95LatencyMs > 60000 {
		return errors.New("max_p95_latency_ms must be greater than 0 and at most 60000 ms")
	}

	return nil
}
