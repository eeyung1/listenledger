package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/eeyung1/listenledger-backend/models"
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
