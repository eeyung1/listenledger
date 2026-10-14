package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPilotTrialEvaluation(t *testing.T) {
	runner, err := NewRunner(t)
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}

	ctx := context.Background()
	metrics, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("trial evaluation run failed: %v", err)
	}

	gates := DefaultTargetGates

	// Assert pre-agreed gates
	if metrics.FullTransactionAcc < gates.MinFullTransactionAccuracy {
		t.Errorf("Full transaction accuracy %.1f%% below gate %.1f%%", metrics.FullTransactionAcc, gates.MinFullTransactionAccuracy)
	}
	if metrics.SKUAccuracy < gates.MinSKUAccuracy {
		t.Errorf("SKU accuracy %.1f%% below gate %.1f%%", metrics.SKUAccuracy, gates.MinSKUAccuracy)
	}
	if metrics.QuantityAccuracy < gates.MinQuantityAccuracy {
		t.Errorf("Quantity accuracy %.1f%% below gate %.1f%%", metrics.QuantityAccuracy, gates.MinQuantityAccuracy)
	}
	if metrics.UnitAccuracy < gates.MinUnitAccuracy {
		t.Errorf("Unit accuracy %.1f%% below gate %.1f%%", metrics.UnitAccuracy, gates.MinUnitAccuracy)
	}
	if metrics.AmountAccuracy < gates.MinAmountAccuracy {
		t.Errorf("Amount accuracy %.1f%% below gate %.1f%%", metrics.AmountAccuracy, gates.MinAmountAccuracy)
	}
	if metrics.FalseCaptureRate > gates.MaxFalseCaptureRate {
		t.Errorf("False capture rate %.1f%% above gate %.1f%%", metrics.FalseCaptureRate, gates.MaxFalseCaptureRate)
	}
	if metrics.MissedTransactionRate > gates.MaxMissedTransactionRate {
		t.Errorf("Missed transaction rate %.1f%% above gate %.1f%%", metrics.MissedTransactionRate, gates.MaxMissedTransactionRate)
	}
	if metrics.CorrectionRate > gates.MaxCorrectionRate {
		t.Errorf("Correction rate %.1f%% above gate %.1f%%", metrics.CorrectionRate, gates.MaxCorrectionRate)
	}
	if metrics.ProtocolComplianceRate < gates.MinProtocolComplianceRate {
		t.Errorf("Protocol compliance rate %.1f%% below gate %.1f%%", metrics.ProtocolComplianceRate, gates.MinProtocolComplianceRate)
	}
	if metrics.StockDiscrepancyCount != 0 {
		t.Errorf("Inventory reconciliation failed: %d discrepancies found", metrics.StockDiscrepancyCount)
	}
	if metrics.RevenueDiscrepancyAmount != 0 {
		t.Errorf("Revenue reconciliation failed: %d kobo discrepancy found", metrics.RevenueDiscrepancyAmount)
	}

	// Generate markdown report and write to root TRIAL_REPORT.md
	report := GenerateMarkdownReport(metrics, gates)
	reportPath := filepath.Join("..", "..", "TRIAL_REPORT.md")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		t.Logf("failed to write TRIAL_REPORT.md: %v", err)
	} else {
		t.Logf("TRIAL_REPORT.md written successfully.")
	}
}
