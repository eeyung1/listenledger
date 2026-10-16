package models

import "time"

// TransactionType distinguishes the two directions inventory moves. Blueprint
// §10 treats both as core V1 features, not future enhancements: a sale removes
// stock, a restock adds it.
type TransactionType string

const (
	// TypeSale is STOCK OUT: a customer purchase captured from the sales
	// conversation. It decrements inventory and increments revenue.
	TypeSale TransactionType = "sale"
	// TypeRestock is STOCK IN: a supplier purchase, captured by the
	// storekeeper reading the receipt aloud. It increments inventory and
	// records purchase cost.
	TypeRestock TransactionType = "restock"
)

// Valid reports whether t is one of the two directions the system records.
func (t TransactionType) Valid() bool {
	switch t {
	case TypeSale, TypeRestock:
		return true
	default:
		return false
	}
}

// TransactionStatus is the lifecycle state of a record, per blueprint §11.
//
// A transaction is only ever written against live inventory when it reaches
// StatusConfirmed. Anything the validation engine is not confident about is
// parked in StatusReviewRequired rather than silently applied.
type TransactionStatus string

const (
	// StatusConfirmed means the capture passed validation and its effect on
	// inventory and revenue is applied. A restock reaching this state is the
	// blueprint's "Received" status.
	StatusConfirmed TransactionStatus = "confirmed"
	// StatusReviewRequired means recognition confidence fell below the
	// configured threshold. The record is held, with no inventory effect,
	// until a human confirms or corrects it in the Review Queue.
	StatusReviewRequired TransactionStatus = "review_required"
	// StatusRejected means a human reviewed the record and discarded it as a
	// false positive. It has no inventory effect.
	StatusRejected TransactionStatus = "rejected"
	// StatusCancelled means a previously confirmed transaction was voided
	// after the fact, e.g. a customer returned the goods. Reversing its
	// inventory effect is the caller's responsibility.
	StatusCancelled TransactionStatus = "cancelled"
)

// Valid reports whether s is one of the four states defined by the blueprint.
func (s TransactionStatus) Valid() bool {
	switch s {
	case StatusConfirmed, StatusReviewRequired, StatusRejected, StatusCancelled:
		return true
	default:
		return false
	}
}

// AffectsInventory reports whether a transaction in this state has already
// been applied to stock levels. It is the guard the inventory engine uses so
// that resolving a review does not double-count.
func (s TransactionStatus) AffectsInventory() bool {
	return s == StatusConfirmed
}

// Confidence holds the recognition confidence for a capture, as a fraction
// between 0 and 1.
//
// Blueprint §11 scores each anchor separately — a product match at 55% with a
// quantity at 98% is Review Required — so the per-field scores are what the
// Review Queue highlights for the owner. Overall is the single value compared
// against the configured threshold; when a per-field score is absent it is
// nil rather than zero, because "not scored" and "scored zero" must not be
// confused when the threshold is applied.
type Confidence struct {
	// Overall is the aggregate score driving the confirm/review decision.
	Overall float64 `json:"overall"`
	// Product is the score for the product/SKU match.
	Product *float64 `json:"product,omitempty"`
	// Quantity is the score for quantity extraction.
	Quantity *float64 `json:"quantity,omitempty"`
	// Amount is the score for amount extraction.
	Amount *float64 `json:"amount,omitempty"`
}

// Transaction is one captured movement of stock, in either direction.
//
// It records what was heard and what it resolved to. ProductID is empty until
// the text has been matched against the catalogue, which is why a
// Review Required transaction is still a complete, storable record: the raw
// capture is preserved for a human to resolve, and ProductName retains what
// was actually said.
type Transaction struct {
	// ID is the backend-assigned stable identifier.
	ID string `json:"id"`
	// ShopID is the business this movement belongs to. It also decides which
	// dashboards receive the event, so it must be set from the authenticated
	// capture source, never from client-supplied data.
	ShopID string `json:"shop_id"`
	// Type is the inventory direction: sale or restock.
	Type TransactionType `json:"type"`
	// ProductID is the resolved catalogue product. Empty when the capture
	// could not be matched and is awaiting review.
	ProductID string `json:"product_id,omitempty"`
	// ProductName is the product as captured or transcribed, whether or not
	// it resolved. For a restock it is the item read off the receipt.
	ProductName string `json:"product_name"`
	// Quantity is how many units moved.
	Quantity int `json:"quantity"`
	// Unit is the packaging Quantity is expressed in, e.g. UnitPack.
	Unit Unit `json:"unit"`
	// Amount is the transaction value: the sale price for a sale, or the
	// purchase cost for a restock.
	Amount Money `json:"amount"`
	// Confidence holds the recognition scores for this capture.
	Confidence Confidence `json:"confidence"`
	// Status is the lifecycle state.
	Status TransactionStatus `json:"status"`
	// OccurredAt is when the sale or delivery happened at the shop.
	OccurredAt time.Time `json:"occurred_at"`
	// CreatedAt is when the record was written.
	CreatedAt time.Time `json:"created_at"`
	// Transcript is the utterance the capture was extracted from. Retained so
	// a reviewer can adjudicate a low-confidence record, and subject to the
	// audio/transcript retention policy in blueprint §17.
	Transcript string `json:"transcript,omitempty"`
	// ReceiptID links multiple line items captured from a single spoken receipt.
	ReceiptID string `json:"receipt_id,omitempty"`
	// SupplierName attributes the source of delivered stock for restocks (blueprint §18).
	SupplierName string `json:"supplier_name,omitempty"`
}
