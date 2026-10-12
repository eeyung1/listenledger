package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

// reviewThreshold is the recognition confidence below which a capture is held
// for a human rather than recorded as confirmed.
//
// Blueprint §11: a 98% product match is recorded automatically, while a 55%
// product match with a 98% quantity is Review Required. It is one global for
// the pilot; per-shop configuration is the eventual home for it, which is why
// it is a named constant rather than a literal at the comparison.
const reviewThreshold = 0.75

// handleTransactions serves the ledger endpoint.
//
//	GET  /api/transactions?shop_id=shop-1&limit=50   — the snapshot
//	POST /api/transactions?shop_id=shop-1            — record a capture
//
// GET and POST share a path because they are two halves of one resource: the
// snapshot the dashboard loads on first paint, and the record that changes it.
func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listTransactions(w, r)
	case http.MethodPost:
		s.createTransaction(w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// listTransactions serves GET /api/transactions — the ledger snapshot.
func (s *Server) listTransactions(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	limit, ok := s.limitParam(w, r)
	if !ok {
		return
	}

	// The request context, not a background one: if the dashboard navigates
	// away or the connection drops mid-query, the work is abandoned rather
	// than left to finish for nobody.
	transactions, err := s.store.GetTransactions(r.Context(), shopID, limit)
	if err != nil {
		s.fail(w, r, "list transactions", err)
		return
	}

	writeJSON(w, http.StatusOK, transactionsResponse{
		ShopID:       shopID,
		Transactions: transactions,
		Count:        len(transactions),
		Limit:        limit,
		GeneratedAt:  time.Now().UTC(),
	})
}

// createTransactionRequest is the body of POST /api/transactions — the payload
// the Rust edge node sends once it has extracted the anchors from speech.
//
// It carries the structured result of recognition, not audio and not raw text.
// The transcript rides along so a human can adjudicate a held capture, which is
// what makes a low-confidence record still worth storing.
//
// shop_id is absent by design: the shop comes from the resolver, so a device
// cannot file a movement against another business's ledger.
type createTransactionRequest struct {
	Type        models.TransactionType   `json:"type"`
	ProductID   string                   `json:"product_id"`
	ProductName string                   `json:"product_name"`
	Quantity    int                      `json:"quantity"`
	Unit        models.Unit              `json:"unit"`
	Amount      models.Money             `json:"amount"`
	Confidence  *models.Confidence       `json:"confidence"`
	Status      models.TransactionStatus `json:"status"`
	OccurredAt  *time.Time               `json:"occurred_at"`
	Transcript  string                   `json:"transcript"`
}

// handleCreate serves POST /api/transactions — recording a capture.
//
//	curl -X POST 'localhost:8080/api/transactions?shop_id=shop-1' \
//	  -d '{"type":"sale","product_name":"Milo","quantity":2,"unit":"pack",
//	       "amount":{"amount":400000,"currency":"NGN"},
//	       "confidence":{"overall":0.98,"product":0.98,"quantity":0.99,"amount":0.98},
//	       "transcript":"Two Milo, four thousand naira. Thank you."}'
//
// Money is in minor units (kobo): NGN 4,000 is 400000.
//
// NOTE: this records the movement and announces it. It does not yet adjust the
// product's stock level. Applying the inventory effect has to happen in the
// same database transaction as the insert, or a crash between them either loses
// a sale or double-counts one — see TODO.md.
func (s *Server) createTransaction(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var body createTransactionRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	transaction, err := body.transaction(shopID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// A product_id the shop does not own would otherwise fail on the composite
	// foreign key, surfacing as a 500 that tells the caller nothing. Checking
	// here turns it into the client error it actually is.
	if transaction.ProductID != "" {
		if _, err := s.store.GetProductByID(r.Context(), shopID, transaction.ProductID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "product_id does not match a product in this shop")
				return
			}
			s.fail(w, r, "look up product", err)
			return
		}
	}

	if err := s.store.InsertTransaction(r.Context(), transaction); err != nil {
		s.fail(w, r, "insert transaction", err)
		return
	}

	// Read back rather than echo the request: the response is then exactly what
	// was stored, with the timestamps the database holds.
	stored, err := s.store.GetTransactionByID(r.Context(), shopID, transaction.ID)
	if err != nil {
		s.fail(w, r, "read back transaction", err)
		return
	}

	// The event type follows the status: a held capture is announced as such so
	// the Review Queue can render it without waiting for a snapshot refetch.
	eventType := ws.EventTransactionCreated
	if stored.Status == models.StatusReviewRequired {
		eventType = ws.EventTransactionReviewRequired
	}
	s.publish(w, r, shopID, eventType, stored)

	writeJSON(w, http.StatusCreated, stored)
}

// transaction validates the request and builds the record to store.
func (b createTransactionRequest) transaction(shopID string) (models.Transaction, error) {
	if !b.Type.Valid() {
		// Not defaulted to "sale": guessing the direction of a stock movement
		// is exactly the silent guess blueprint §11 forbids, and getting it
		// backwards is invisible until the stock count is wrong.
		return models.Transaction{}, errors.New("type must be \"sale\" or \"restock\"")
	}

	productName := strings.TrimSpace(b.ProductName)
	if productName == "" {
		return models.Transaction{}, errors.New("product_name is required")
	}

	if b.Quantity <= 0 {
		return models.Transaction{}, errors.New("quantity must be a positive integer")
	}

	if !b.Unit.Valid() {
		return models.Transaction{}, errors.New("unit is not a recognised unit")
	}

	currency := strings.TrimSpace(strings.ToUpper(b.Amount.Currency))
	if currency == "" {
		return models.Transaction{}, errors.New("amount.currency is required")
	}
	if b.Amount.Amount < 0 {
		return models.Transaction{}, errors.New("amount.amount cannot be negative")
	}

	status := b.Status
	if status == "" {
		status = deriveStatus(b.Confidence)
	} else if !status.Valid() {
		return models.Transaction{}, errors.New("status is not a recognised state")
	}

	occurredAt := time.Now().UTC()
	if b.OccurredAt != nil {
		occurredAt = b.OccurredAt.UTC()
	}

	id, err := newID("txn")
	if err != nil {
		return models.Transaction{}, err
	}

	confidence := models.Confidence{}
	if b.Confidence != nil {
		confidence = *b.Confidence
	}

	return models.Transaction{
		ID:          id,
		ShopID:      shopID,
		Type:        b.Type,
		ProductID:   strings.TrimSpace(b.ProductID),
		ProductName: productName,
		Quantity:    b.Quantity,
		Unit:        b.Unit,
		Amount:      models.Money{Amount: b.Amount.Amount, Currency: currency},
		Confidence:  confidence,
		Status:      status,
		OccurredAt:  occurredAt,
		CreatedAt:   time.Now().UTC(),
		Transcript:  b.Transcript,
	}, nil
}

// deriveStatus applies the confidence threshold to decide whether a capture is
// recorded outright or held for a human.
//
// The weakest anchor decides, not the average: blueprint §11 holds a capture
// whose product matched at 55% while its quantity matched at 98%, because the
// average would have hidden the one field the system actually doubted.
//
// A capture with no score at all is held rather than trusted — "we did not
// measure this" is not the same claim as "this is correct", and treating the
// two as equal is how an unscored guess reaches the ledger unchallenged.
func deriveStatus(confidence *models.Confidence) models.TransactionStatus {
	if confidence == nil {
		return models.StatusReviewRequired
	}

	lowest := confidence.Overall
	scored := confidence.Overall > 0
	for _, score := range []*float64{confidence.Product, confidence.Quantity, confidence.Amount} {
		if score == nil {
			continue
		}
		if !scored || *score < lowest {
			lowest = *score
			scored = true
		}
	}

	if !scored || lowest < reviewThreshold {
		return models.StatusReviewRequired
	}
	return models.StatusConfirmed
}
