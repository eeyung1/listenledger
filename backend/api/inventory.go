package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/eeyung1/listenledger-backend/storage"
)

// handleInventory serves GET /api/inventory — the catalogue and its stock.
//
//	GET /api/inventory?shop_id=shop-1&q=milo&status=active&limit=50&offset=0
//
// Supports server-side search (q), status filtering (active|archived|all), and pagination.
func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	urlQuery := r.URL.Query()
	qText := urlQuery.Get("q")
	status := urlQuery.Get("status")

	var limit, offset int
	if l := urlQuery.Get("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 {
			limit = val
		}
	}
	if o := urlQuery.Get("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val >= 0 {
			offset = val
		}
	}

	products, total, err := s.store.QueryProducts(r.Context(), shopID, storage.ProductQuery{
		Search: qText,
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		s.fail(w, r, "query inventory", err)
		return
	}

	lowStock := 0
	for _, product := range products {
		if !product.IsArchived() && product.IsLowStock() {
			lowStock++
		}
	}

	writeJSON(w, http.StatusOK, inventoryResponse{
		ShopID:        shopID,
		Products:      products,
		Count:         len(products),
		Total:         total,
		LowStockCount: lowStock,
		LastSeq:       s.lastSeq(shopID),
		GeneratedAt:   time.Now().UTC(),
	})
}
