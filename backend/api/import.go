package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

// handleProductsTemplate returns a downloadable CSV template for catalogue onboarding.
func (s *Server) handleProductsTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="catalogue_template.csv"`)

	buf := &bytes.Buffer{}
	writer := csv.NewWriter(buf)

	_ = writer.Write([]string{
		"sku", "name", "price_amount", "price_currency", "quantity",
		"base_unit", "aliases", "packaging", "low_stock_threshold",
	})
	_ = writer.Write([]string{
		"MILO-400", "Milo 400g", "250000", "NGN", "50",
		"pack", "Milo;Big Milo", "carton:12", "10",
	})
	_ = writer.Write([]string{
		"PEAK-400", "Peak Milk 400g", "350000", "NGN", "40",
		"tin", "Peak;Big Peak", "", "5",
	})
	_ = writer.Write([]string{
		"INDOMIE-70", "Indomie Chicken 70g", "15000", "NGN", "120",
		"pack", "Indomie;Noodles", "carton:40", "20",
	})

	writer.Flush()
	_, _ = w.Write(buf.Bytes())
}

type importResult struct {
	Total    int      `json:"total"`
	Imported int      `json:"imported"`
	Updated  int      `json:"updated"`
	Failed   int      `json:"failed"`
	Errors   []string `json:"errors"`
}

// handleProductsImport allows onboarding or bulk updating a shop catalogue from JSON or CSV.
func (s *Server) handleProductsImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	contentType := r.Header.Get("Content-Type")
	var items []createProductRequest
	var parseErr error

	if strings.HasPrefix(contentType, "application/json") {
		items, parseErr = parseJSONImport(r.Body)
	} else if strings.HasPrefix(contentType, "multipart/form-data") {
		// 10 MB maximum upload
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required in multipart form")
			return
		}
		defer file.Close()
		items, parseErr = parseCSVImport(file)
	} else {
		// Treat as plain text/csv or fallback
		items, parseErr = parseCSVImport(r.Body)
	}

	if parseErr != nil {
		writeError(w, http.StatusBadRequest, parseErr.Error())
		return
	}

	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "import payload contains no products")
		return
	}

	res := importResult{Total: len(items), Errors: make([]string, 0)}

	for idx, item := range items {
		prod, err := item.product(shopID)
		if err != nil {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d (%s): %s", idx+1, item.SKU, err.Error()))
			continue
		}

		// Check if product exists by SKU
		existing, err := s.store.GetProductBySKU(r.Context(), shopID, prod.SKU)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			res.Failed++
			res.Errors = append(res.Errors, fmt.Sprintf("row %d (%s): query failed: %s", idx+1, prod.SKU, err.Error()))
			continue
		}

		if errors.Is(err, storage.ErrNotFound) {
			// Insert new product
			if err := s.store.InsertProduct(r.Context(), prod); err != nil {
				res.Failed++
				res.Errors = append(res.Errors, fmt.Sprintf("row %d (%s): insert failed: %s", idx+1, prod.SKU, err.Error()))
				continue
			}
			res.Imported++
			s.publish(w, r, shopID, ws.EventProductCreated, prod)
		} else {
			// Update existing product
			existing.Name = prod.Name
			existing.Aliases = prod.Aliases
			existing.Price = prod.Price
			existing.BaseUnit = prod.BaseUnit
			existing.Packaging = prod.Packaging
			existing.LowStockThreshold = prod.LowStockThreshold
			updated, err := s.store.UpdateProduct(r.Context(), existing)
			if err != nil {
				res.Failed++
				res.Errors = append(res.Errors, fmt.Sprintf("row %d (%s): update failed: %s", idx+1, prod.SKU, err.Error()))
				continue
			}
			res.Updated++
			s.publish(w, r, shopID, ws.EventInventoryUpdated, updated)
		}
	}

	writeJSON(w, http.StatusOK, res)
}

func parseJSONImport(reader io.Reader) ([]createProductRequest, error) {
	var items []createProductRequest
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("invalid JSON array: %w", err)
	}
	return items, nil
}

func parseCSVImport(reader io.Reader) ([]createProductRequest, error) {
	r := csv.NewReader(reader)
	r.TrimLeadingSpace = true

	headers, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	colIdx := make(map[string]int)
	for i, h := range headers {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	requiredCols := []string{"sku", "name", "price_amount", "base_unit"}
	for _, req := range requiredCols {
		if _, ok := colIdx[req]; !ok {
			return nil, fmt.Errorf("missing required CSV header column: %s", req)
		}
	}

	var items []createProductRequest
	rowNum := 1

	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV read error at row %d: %w", rowNum+1, err)
		}
		rowNum++

		getField := func(col string) string {
			if idx, ok := colIdx[col]; ok && idx < len(record) {
				return strings.TrimSpace(record[idx])
			}
			return ""
		}

		sku := getField("sku")
		name := getField("name")
		if sku == "" && name == "" {
			continue // skip blank lines
		}

		priceAmt, err := strconv.ParseInt(getField("price_amount"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid price_amount at row %d: %w", rowNum, err)
		}

		currency := getField("price_currency")
		if currency == "" {
			currency = "NGN"
		}

		qty := 0
		if rawQty := getField("quantity"); rawQty != "" {
			if q, err := strconv.Atoi(rawQty); err == nil && q >= 0 {
				qty = q
			}
		}

		threshold := 5
		if rawTh := getField("low_stock_threshold"); rawTh != "" {
			if th, err := strconv.Atoi(rawTh); err == nil && th >= 0 {
				threshold = th
			}
		}

		baseUnit := models.Unit(strings.ToLower(getField("base_unit")))

		var aliases []string
		if rawAliases := getField("aliases"); rawAliases != "" {
			parts := strings.Split(rawAliases, ";")
			for _, p := range parts {
				if t := strings.TrimSpace(p); t != "" {
					aliases = append(aliases, t)
				}
			}
		}

		var packaging []models.PackagingRelation
		if rawPkg := getField("packaging"); rawPkg != "" {
			// format: "carton:12,crate:24" or "carton:12"
			parts := strings.Split(rawPkg, ",")
			for _, p := range parts {
				kv := strings.Split(strings.TrimSpace(p), ":")
				if len(kv) == 2 {
					unit := models.Unit(strings.ToLower(strings.TrimSpace(kv[0])))
					factor, err := strconv.Atoi(strings.TrimSpace(kv[1]))
					if err == nil && factor > 1 && unit.Valid() {
						packaging = append(packaging, models.PackagingRelation{
							From:   unit,
							To:     baseUnit,
							Factor: factor,
						})
					}
				}
			}
		}

		items = append(items, createProductRequest{
			SKU:               sku,
			Name:              name,
			Price:             models.Money{Amount: priceAmt, Currency: currency},
			Quantity:          qty,
			BaseUnit:          baseUnit,
			Aliases:           aliases,
			Packaging:         packaging,
			LowStockThreshold: threshold,
		})
	}

	return items, nil
}

