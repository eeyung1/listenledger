// Package models defines the core data structures shared across the
// ListenLedger backend orchestrator.
//
// The types here describe the truth the business owns: its catalogue and the
// transactions recorded against it. They are transport-agnostic — the HTTP and
// WebSocket layers are expected to serialise these structs directly, so the
// JSON tags are part of the contract with the frontend SPA.
package models

import "time"

// Currency codes are ISO 4217. NGN (Nigerian naira) is the V1 default per the
// pilot scope, but the field is carried explicitly so multi-currency does not
// require a schema change later.
const (
	CurrencyNGN = "NGN"
)

// Money is a monetary amount held in the *minor* unit of its currency
// (kobo for NGN, cents for USD). Integer storage avoids the rounding drift
// that float64 introduces when prices are summed across a trading day.
type Money struct {
	// Amount is denominated in the minor unit: NGN 4,000 is Amount 400000.
	Amount int64 `json:"amount"`
	// Currency is the ISO 4217 code, e.g. CurrencyNGN.
	Currency string `json:"currency"`
}

// Unit is the packaging a quantity is counted in. Blueprint §10 requires the
// inventory engine to distinguish "5 packs" from "5 bottles", so every
// quantity in the system is meaningless without one.
type Unit string

const (
	UnitPiece   Unit = "piece"
	UnitPack    Unit = "pack"
	UnitCarton  Unit = "carton"
	UnitBottle  Unit = "bottle"
	UnitCrate   Unit = "crate"
	UnitTin     Unit = "tin"
	UnitBag     Unit = "bag"
	UnitSachet  Unit = "sachet"
	UnitRoll    Unit = "roll"
	UnitPainter Unit = "painter"
	UnitMudu    Unit = "mudu"
	UnitDerica  Unit = "derica"
	UnitRubber  Unit = "rubber"
)

// Valid reports whether u is one of the units the inventory engine knows how
// to convert between.
//
// The vocabulary is closed for the same reason the transaction statuses are:
// a quantity is meaningless without a unit, and an unrecognised one would sit
// in the ledger unable to be converted into a product's base unit.
func (u Unit) Valid() bool {
	switch u {
	case UnitPiece, UnitPack, UnitCarton, UnitBottle, UnitCrate,
		UnitTin, UnitBag, UnitSachet, UnitRoll,
		UnitPainter, UnitMudu, UnitDerica, UnitRubber:
		return true
	default:
		return false
	}
}

// PackagingRelation records a containment rule for one product, e.g.
// {From: UnitCarton, To: UnitPack, Factor: 12} reads as "1 carton = 12 packs".
//
// These relationships are business- and product-specific (blueprint §10) and
// are what allow a restock of "three cartons" to increase stock measured in
// packs. Conversion is the inventory engine's job; this type only stores the
// rule.
type PackagingRelation struct {
	// From is the larger unit.
	From Unit `json:"from"`
	// To is the smaller unit.
	To Unit `json:"to"`
	// Factor is how many To units one From unit contains.
	Factor int `json:"factor"`
}

// Product is a single catalogue entry, owned by one business.
//
// Aliases carry the shop's local jargon — the pronunciations and shorthand the
// validation engine must map back to the official product (blueprint §9, e.g.
// "Big Peak" -> "Peak Milk 400g"). They are the mechanism by which recognition
// improves without retraining the speech model.
type Product struct {
	// ID is the backend-assigned stable identifier.
	ID string `json:"id"`
	// ShopID is the business that owns this catalogue entry. Every read and
	// write must be scoped by it: two shops can legitimately stock the same
	// SKU, and neither may see the other's aliases or stock levels.
	ShopID string `json:"shop_id"`
	// Name is the official product name, e.g. "Peak Milk 400g".
	Name string `json:"name"`
	// SKU is the merchant-facing stock keeping unit, unique per business.
	SKU string `json:"sku"`
	// Aliases are alternative names and local pronunciations that resolve to
	// this product.
	Aliases []string `json:"aliases"`
	// Price is the current selling price per BaseUnit.
	Price Money `json:"price"`
	// Quantity is the current stock on hand, expressed in BaseUnit.
	Quantity int `json:"quantity"`
	// BaseUnit is the unit Quantity is counted in. All other units in
	// Packaging are converted into this one before stock is adjusted.
	BaseUnit Unit `json:"base_unit"`
	// Packaging holds this product's containment rules.
	Packaging []PackagingRelation `json:"packaging,omitempty"`
	// LowStockThreshold is the level at or below which the dashboard raises a
	// low-stock alert. Zero means "not configured".
	LowStockThreshold int `json:"low_stock_threshold,omitempty"`
	// ArchivedAt records when this product was soft-archived/discontinued.
	// Nil for active catalogue items.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

// IsLowStock reports whether the product has reached its configured reorder
// level. A threshold of zero means no alert is configured, so the product
// never reports as low.
func (p Product) IsLowStock() bool {
	return p.LowStockThreshold > 0 && p.Quantity <= p.LowStockThreshold
}

// IsArchived reports whether the product has been archived.
func (p Product) IsArchived() bool {
	return p.ArchivedAt != nil
}
