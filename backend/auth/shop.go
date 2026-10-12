// Package auth resolves the identity behind a request.
//
// It exists as its own package because two surfaces need the same answer —
// the WebSocket upgrade and the REST snapshot endpoints — and they must never
// disagree about which shop a caller owns. When real authentication lands,
// this is the only package that changes.
package auth

import (
	"errors"
	"net/http"
	"strings"
)

// ShopResolver decides which business a request belongs to.
//
// This is the most security-sensitive function in the backend: the returned
// ShopID decides what data is read and which dashboards receive events, so
// resolving it from attacker-controlled input means one shop can read
// another's ledger.
type ShopResolver func(*http.Request) (string, error)

// QueryShopResolver takes the shop from the ?shop_id= query parameter.
//
// DEVELOPMENT ONLY. Any caller can claim any shop. It exists so a single-shop
// pilot can be exercised before login exists, and it must be replaced by a
// resolver that reads an authenticated session — a signed cookie, or a paired
// edge-device credential — before a real shop's data is stored.
func QueryShopResolver(r *http.Request) (string, error) {
	shopID := strings.TrimSpace(r.URL.Query().Get("shop_id"))
	if shopID == "" {
		return "", errors.New("no shop_id supplied")
	}
	return shopID, nil
}
