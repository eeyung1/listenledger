package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

// createProductRequest is the body of POST /api/products.
//
// shop_id is absent by design: the shop comes from the resolver, never from the
// body. A caller able to name the shop it writes into is a caller able to write
// into someone else's catalogue.
type createProductRequest struct {
	Name              string                     `json:"name"`
	SKU               string                     `json:"sku"`
	Aliases           []string                   `json:"aliases"`
	Price             models.Money               `json:"price"`
	Quantity          int                        `json:"quantity"`
	BaseUnit          models.Unit                `json:"base_unit"`
	Packaging         []models.PackagingRelation `json:"packaging"`
	LowStockThreshold int                        `json:"low_stock_threshold"`
}

// handleProducts serves POST /api/products — adding a catalogue entry.
//
//	curl -X POST 'localhost:8080/api/products?shop_id=shop-1' \
//	  -d '{"name":"Peak Milk 400g","sku":"PEAK-400","base_unit":"piece",
//	       "price":{"amount":350000,"currency":"NGN"},"quantity":24,
//	       "aliases":["Peak","Big Peak"]}'
//
// This is where a product captured from voice but not recognised is confirmed
// into the catalogue, which is how recognition improves: the alias list is the
// shop's own vocabulary, not something the speech model has to relearn.
//
// Money is in minor units (kobo), matching models.Money — NGN 3,500 is 350000.
func (s *Server) handleProducts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var body createProductRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	product, err := body.product(shopID)
	if err != nil {
		// Every message here describes the caller's own input, so it is safe
		// to return verbatim; none of them name a table, row or file.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate duplicate SKUs and ambiguous aliases against existing catalogue
	existingProducts, err := s.store.ListProducts(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "list catalogue for conflict validation", err)
		return
	}
	if err := validateCatalogueConflicts(existingProducts, product.Name, product.SKU, product.Aliases, ""); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if err := s.store.InsertProduct(r.Context(), product); err != nil {
		s.fail(w, r, "insert product", err)
		return
	}

	s.publish(w, r, shopID, ws.EventProductCreated, product)
	writeJSON(w, http.StatusCreated, product)
}

// product validates the request and builds the record to store.
func (b createProductRequest) product(shopID string) (models.Product, error) {
	name := strings.TrimSpace(b.Name)
	if name == "" {
		return models.Product{}, errors.New("name is required")
	}

	sku := strings.TrimSpace(b.SKU)
	if sku == "" {
		return models.Product{}, errors.New("sku is required")
	}

	if !b.BaseUnit.Valid() {
		return models.Product{}, errors.New("base_unit is not a recognised unit")
	}

	currency := strings.TrimSpace(strings.ToUpper(b.Price.Currency))
	if currency == "" {
		return models.Product{}, errors.New("price.currency is required")
	}
	if b.Price.Amount < 0 {
		return models.Product{}, errors.New("price.amount cannot be negative")
	}
	if b.Quantity < 0 {
		return models.Product{}, errors.New("quantity cannot be negative")
	}
	if b.LowStockThreshold < 0 {
		return models.Product{}, errors.New("low_stock_threshold cannot be negative")
	}

	if err := models.ValidatePackaging(b.BaseUnit, b.Packaging); err != nil {
		return models.Product{}, err
	}
	for _, relation := range b.Packaging {
		if !relation.From.Valid() || !relation.To.Valid() {
			return models.Product{}, errors.New("packaging from/to must be recognised units")
		}
		if relation.Factor <= 0 {
			return models.Product{}, errors.New("packaging factor must be positive")
		}
	}

	// Non-nil so a product with no aliases encodes as [] rather than null: the
	// dashboard iterates this, and null would need a special case there.
	aliases := make([]string, 0, len(b.Aliases))
	for _, alias := range b.Aliases {
		if trimmed := strings.TrimSpace(alias); trimmed != "" {
			aliases = append(aliases, trimmed)
		}
	}

	id, err := newID("prd")
	if err != nil {
		return models.Product{}, err
	}

	return models.Product{
		ID:                id,
		ShopID:            shopID,
		Name:              name,
		SKU:               sku,
		Aliases:           aliases,
		Price:             models.Money{Amount: b.Price.Amount, Currency: currency},
		Quantity:          b.Quantity,
		BaseUnit:          b.BaseUnit,
		Packaging:         b.Packaging,
		LowStockThreshold: b.LowStockThreshold,
	}, nil
}

// validateCatalogueConflicts checks for duplicate SKU, duplicate product name, or ambiguous aliases.
func validateCatalogueConflicts(products []models.Product, name, sku string, aliases []string, excludeID string) error {
	trimmedName := strings.TrimSpace(name)
	trimmedSKU := strings.TrimSpace(sku)

	seenAliases := make(map[string]struct{})
	for _, alias := range aliases {
		a := strings.ToLower(strings.TrimSpace(alias))
		if a == "" {
			continue
		}
		if a == strings.ToLower(trimmedName) {
			return fmt.Errorf("alias '%s' cannot be identical to product name", alias)
		}
		if a == strings.ToLower(trimmedSKU) {
			return fmt.Errorf("alias '%s' cannot be identical to product SKU", alias)
		}
		if _, seen := seenAliases[a]; seen {
			return fmt.Errorf("duplicate alias '%s' within product definition", alias)
		}
		seenAliases[a] = struct{}{}
	}

	for _, p := range products {
		if p.ID == excludeID {
			continue
		}
		if trimmedSKU != "" && strings.EqualFold(p.SKU, trimmedSKU) {
			return fmt.Errorf("SKU '%s' is already used by product '%s'", sku, p.Name)
		}
		if trimmedName != "" && strings.EqualFold(p.Name, trimmedName) {
			return fmt.Errorf("product name '%s' already exists in catalogue (SKU: %s)", name, p.SKU)
		}
		for _, alias := range aliases {
			trimmedAlias := strings.TrimSpace(alias)
			if trimmedAlias == "" {
				continue
			}
			if strings.EqualFold(p.Name, trimmedAlias) {
				return fmt.Errorf("alias '%s' conflicts with existing product name '%s'", alias, p.Name)
			}
			if strings.EqualFold(p.SKU, trimmedAlias) {
				return fmt.Errorf("alias '%s' conflicts with existing product SKU '%s'", alias, p.SKU)
			}
			for _, otherAlias := range p.Aliases {
				if strings.EqualFold(otherAlias, trimmedAlias) {
					return fmt.Errorf("alias '%s' is already assigned to product '%s'", alias, p.Name)
				}
			}
		}
		if trimmedName != "" {
			for _, otherAlias := range p.Aliases {
				if strings.EqualFold(otherAlias, trimmedName) {
					return fmt.Errorf("product name '%s' conflicts with alias already assigned to product '%s'", name, p.Name)
				}
			}
		}
		if trimmedSKU != "" {
			for _, otherAlias := range p.Aliases {
				if strings.EqualFold(otherAlias, trimmedSKU) {
					return fmt.Errorf("product SKU '%s' conflicts with alias already assigned to product '%s'", sku, p.Name)
				}
			}
		}
	}
	return nil
}

// handleProductDetail serves GET and PATCH/PUT on /api/products/{id},
// as well as /api/products/{id}/movements and /api/products/{id}/adjust.
func (s *Server) handleProductDetail(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/products/")
	parts := strings.Split(rel, "/")
	if len(parts) == 1 && parts[0] != "" {
		id := parts[0]
		switch r.Method {
		case http.MethodGet:
			s.getProduct(w, r, id)
		case http.MethodPatch, http.MethodPut:
			s.updateProduct(w, r, id)
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPatch+", "+http.MethodPut)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(parts) == 2 && parts[0] != "" {
		id := parts[0]
		action := parts[1]
		switch action {
		case "movements":
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			s.getProductMovements(w, r, id)
			return
		case "adjust":
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			s.adjustProductStock(w, r, id)
			return
		case "archive":
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			s.archiveProduct(w, r, id)
			return
		case "unarchive":
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			s.unarchiveProduct(w, r, id)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetProductByID(r.Context(), shopID, id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		s.fail(w, r, "get product", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type updateProductRequest struct {
	Name              *string                     `json:"name"`
	SKU               *string                     `json:"sku"`
	Aliases           *[]string                   `json:"aliases"`
	Price             *models.Money               `json:"price"`
	BaseUnit          *models.Unit                `json:"base_unit"`
	Packaging         *[]models.PackagingRelation `json:"packaging"`
	LowStockThreshold *int                        `json:"low_stock_threshold"`
}

func (s *Server) updateProduct(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	existing, err := s.store.GetProductByID(r.Context(), shopID, id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		s.fail(w, r, "get product", err)
		return
	}

	var req updateProductRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		existing.Name = name
	}
	if req.SKU != nil {
		sku := strings.TrimSpace(*req.SKU)
		if sku == "" {
			writeError(w, http.StatusBadRequest, "sku cannot be empty")
			return
		}
		existing.SKU = sku
	}
	if req.Aliases != nil {
		aliases := make([]string, 0, len(*req.Aliases))
		for _, alias := range *req.Aliases {
			if trimmed := strings.TrimSpace(alias); trimmed != "" {
				aliases = append(aliases, trimmed)
			}
		}
		existing.Aliases = aliases
	}
	if req.Price != nil {
		if req.Price.Amount < 0 {
			writeError(w, http.StatusBadRequest, "price cannot be negative")
			return
		}
		if req.Price.Currency != "" && req.Price.Currency != existing.Price.Currency {
			writeError(w, http.StatusBadRequest, "currency cannot be changed")
			return
		}
		existing.Price = *req.Price
	}
	if req.BaseUnit != nil {
		if !req.BaseUnit.Valid() {
			writeError(w, http.StatusBadRequest, "base_unit is not a recognised unit")
			return
		}
		if *req.BaseUnit != existing.BaseUnit {
			_, totalMovements, err := s.store.GetProductMovements(r.Context(), shopID, existing.ID, 1, 0)
			if err != nil {
				s.fail(w, r, "check product movements", err)
				return
			}
			if totalMovements > 0 {
				writeError(w, http.StatusConflict, fmt.Sprintf("cannot change base_unit for '%s': product has %d historical stock movements; changing base unit would silently reinterpret past records", existing.Name, totalMovements))
				return
			}
		}
		existing.BaseUnit = *req.BaseUnit
	}
	if req.Packaging != nil {
		if err := models.ValidatePackaging(existing.BaseUnit, *req.Packaging); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for _, relation := range *req.Packaging {
			if !relation.From.Valid() || !relation.To.Valid() {
				writeError(w, http.StatusBadRequest, "packaging from/to must be recognised units")
				return
			}
			if relation.Factor <= 0 {
				writeError(w, http.StatusBadRequest, "packaging factor must be positive")
				return
			}
		}
		existing.Packaging = *req.Packaging
	}
	if req.LowStockThreshold != nil {
		if *req.LowStockThreshold < 0 {
			writeError(w, http.StatusBadRequest, "low_stock_threshold cannot be negative")
			return
		}
		existing.LowStockThreshold = *req.LowStockThreshold
	}

	if req.Name != nil || req.SKU != nil || req.Aliases != nil {
		existingProducts, err := s.store.ListProducts(r.Context(), shopID)
		if err != nil {
			s.fail(w, r, "list catalogue for conflict validation", err)
			return
		}
		if err := validateCatalogueConflicts(existingProducts, existing.Name, existing.SKU, existing.Aliases, existing.ID); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}

	updated, err := s.store.UpdateProduct(r.Context(), existing)
	if err != nil {
		s.fail(w, r, "update product", err)
		return
	}

	s.publish(w, r, shopID, ws.EventInventoryUpdated, updated)
	writeJSON(w, http.StatusOK, updated)
}

type productMovementsResponse struct {
	ShopID    string                    `json:"shop_id"`
	ProductID string                    `json:"product_id"`
	Movements []storage.ProductMovement `json:"movements"`
	Total     int                       `json:"total"`
	Limit     int                       `json:"limit"`
	Offset    int                       `json:"offset"`
}

func (s *Server) getProductMovements(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	limit := 50
	offset := 0
	q := r.URL.Query()
	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		if l, err := strconv.Atoi(rawLimit); err == nil && l > 0 {
			limit = storage.ClampLimit(l)
		}
	}
	if rawOffset := strings.TrimSpace(q.Get("offset")); rawOffset != "" {
		if o, err := strconv.Atoi(rawOffset); err == nil && o >= 0 {
			offset = o
		}
	}

	movements, total, err := s.store.GetProductMovements(r.Context(), shopID, id, limit, offset)
	if err != nil {
		s.fail(w, r, "fetch product movements", err)
		return
	}

	writeJSON(w, http.StatusOK, productMovementsResponse{
		ShopID:    shopID,
		ProductID: id,
		Movements: movements,
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	})
}

type adjustStockRequest struct {
	Quantity int    `json:"quantity"`
	Reason   string `json:"reason"`
}

type adjustStockResponse struct {
	Product     models.Product     `json:"product"`
	Transaction models.Transaction `json:"transaction"`
}

func (s *Server) adjustProductStock(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req adjustStockRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Quantity < 0 {
		writeError(w, http.StatusBadRequest, "quantity cannot be negative")
		return
	}

	product, txn, err := s.store.AdjustProductStock(r.Context(), shopID, id, req.Quantity, req.Reason)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "product not found")
			return
		}
		s.fail(w, r, "adjust product stock", err)
		return
	}

	s.publish(w, r, shopID, ws.EventInventoryUpdated, product)
	if txn.ID != "" {
		s.publish(w, r, shopID, ws.EventTransactionCreated, txn)
	}

	writeJSON(w, http.StatusOK, adjustStockResponse{
		Product:     product,
		Transaction: txn,
	})
}

func (s *Server) archiveProduct(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	product, err := s.store.ArchiveProduct(r.Context(), shopID, id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		s.fail(w, r, "archive product", err)
		return
	}
	s.publish(w, r, shopID, ws.EventInventoryUpdated, product)
	writeJSON(w, http.StatusOK, product)
}

func (s *Server) unarchiveProduct(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	product, err := s.store.UnarchiveProduct(r.Context(), shopID, id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		s.fail(w, r, "unarchive product", err)
		return
	}
	s.publish(w, r, shopID, ws.EventInventoryUpdated, product)
	writeJSON(w, http.StatusOK, product)
}
