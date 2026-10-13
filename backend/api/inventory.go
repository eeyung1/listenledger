package api

import (
	"net/http"
	"time"
)

// handleInventory serves GET /api/inventory — the catalogue and its stock.
//
//	GET /api/inventory?shop_id=shop-1
//
// There is no limit parameter: a shop's catalogue is small enough to return
// whole, and the dashboard's search filters it client-side.
func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	products, err := s.store.ListProducts(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "list inventory", err)
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
		LowStockCount: lowStock,
		LastSeq:       s.lastSeq(shopID),
		GeneratedAt:   time.Now().UTC(),
	})
}
