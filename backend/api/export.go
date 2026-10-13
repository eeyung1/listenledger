package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

// maxExportLimit allows exporting large batches for reconciliation without unbounded memory growth.
const maxExportLimit = 10000

// handleExport handles GET /api/transactions/export.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "csv"
	}
	if format != "csv" {
		writeError(w, http.StatusBadRequest, "unsupported export format: must be csv")
		return
	}

	filter, err := parseTransactionFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Default or expand limit for export up to maxExportLimit
	if filter.Limit == storage.DefaultTransactionLimit {
		filter.Limit = maxExportLimit
	}

	txns, _, err := s.store.QueryTransactions(r.Context(), shopID, filter)
	if err != nil {
		s.fail(w, r, "export transactions", err)
		return
	}

	filename := fmt.Sprintf("listenledger-transactions-%s.csv", time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	writer := csv.NewWriter(w)
	defer writer.Flush()

	headers := []string{
		"Transaction ID",
		"Time (UTC)",
		"Type",
		"Product ID",
		"Product Name",
		"Quantity",
		"Unit",
		"Amount",
		"Currency",
		"Status",
		"Overall Confidence",
		"Transcript",
	}
	if err := writer.Write(headers); err != nil {
		s.log.Error("export: write header", "error", err)
		return
	}

	for _, t := range txns {
		row := []string{
			sanitizeCSVField(t.ID),
			t.OccurredAt.UTC().Format(time.RFC3339),
			sanitizeCSVField(string(t.Type)),
			sanitizeCSVField(t.ProductID),
			sanitizeCSVField(t.ProductName),
			fmt.Sprintf("%d", t.Quantity),
			sanitizeCSVField(string(t.Unit)),
			formatAmountDecimal(t.Amount),
			sanitizeCSVField(t.Amount.Currency),
			sanitizeCSVField(string(t.Status)),
			fmt.Sprintf("%.2f", t.Confidence.Overall),
			sanitizeCSVField(t.Transcript),
		}
		if err := writer.Write(row); err != nil {
			s.log.Error("export: write row", "error", err)
			return
		}
	}
}

// sanitizeCSVField protects spreadsheet consumers against formula injection (OWASP CSV Injection).
// Any value starting with =, +, -, @, \t, or \r is prefixed with a single quote '.
func sanitizeCSVField(val string) string {
	trimmed := strings.TrimSpace(val)
	if len(trimmed) > 0 {
		switch trimmed[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + val
		}
	}
	return val
}

// formatAmountDecimal formats minor-unit integer money into decimal string (e.g. 400000 -> "4000.00").
func formatAmountDecimal(m models.Money) string {
	sign := ""
	amt := m.Amount
	if amt < 0 {
		sign = "-"
		amt = -amt
	}
	units := amt / 100
	cents := amt % 100
	return fmt.Sprintf("%s%d.%02d", sign, units, cents)
}
