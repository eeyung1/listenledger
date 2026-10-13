package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
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

// parseTransactionFilter parses query parameters into a TransactionFilter.
func parseTransactionFilter(r *http.Request) (storage.TransactionFilter, error) {
	q := r.URL.Query()
	filter := storage.TransactionFilter{
		Limit: storage.DefaultTransactionLimit,
	}

	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		l, err := strconv.Atoi(rawLimit)
		if err != nil || l <= 0 {
			return filter, errors.New("limit must be a positive integer")
		}
		filter.Limit = storage.ClampLimit(l)
	}

	if rawOffset := strings.TrimSpace(q.Get("offset")); rawOffset != "" {
		off, err := strconv.Atoi(rawOffset)
		if err != nil || off < 0 {
			return filter, errors.New("offset must be a non-negative integer")
		}
		filter.Offset = off
	}

	if rawType := strings.TrimSpace(q.Get("type")); rawType != "" {
		t := models.TransactionType(rawType)
		if !t.Valid() {
			return filter, fmt.Errorf("invalid type %q", rawType)
		}
		filter.Type = &t
	}

	if rawStatus := strings.TrimSpace(q.Get("status")); rawStatus != "" {
		st := models.TransactionStatus(rawStatus)
		if !st.Valid() {
			return filter, fmt.Errorf("invalid status %q", rawStatus)
		}
		filter.Status = &st
	}

	if productID := strings.TrimSpace(q.Get("product_id")); productID != "" {
		filter.ProductID = productID
	}

	if rawFrom := strings.TrimSpace(q.Get("from")); rawFrom != "" {
		t, err := time.Parse(time.RFC3339, rawFrom)
		if err != nil {
			return filter, errors.New("from must be an RFC3339 timestamp")
		}
		filter.From = &t
	}

	if rawTo := strings.TrimSpace(q.Get("to")); rawTo != "" {
		t, err := time.Parse(time.RFC3339, rawTo)
		if err != nil {
			return filter, errors.New("to must be an RFC3339 timestamp")
		}
		filter.To = &t
	}

	return filter, nil
}

// listTransactions serves GET /api/transactions — the ledger snapshot.
func (s *Server) listTransactions(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	filter, err := parseTransactionFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The request context, not a background one: if the dashboard navigates
	// away or the connection drops mid-query, the work is abandoned rather
	// than left to finish for nobody.
	transactions, total, err := s.store.QueryTransactions(r.Context(), shopID, filter)
	if err != nil {
		s.fail(w, r, "list transactions", err)
		return
	}

	writeJSON(w, http.StatusOK, transactionsResponse{
		ShopID:       shopID,
		Transactions: transactions,
		Count:        len(transactions),
		Total:        total,
		Limit:        filter.Limit,
		Offset:       filter.Offset,
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
// Ledger and stock commit atomically. An Idempotency-Key is required so retries
// return the first result instead of applying stock a second time.
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

	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 {
		writeError(w, 400, "Idempotency-Key is required (maximum 128 bytes)")
		return
	}
	canonical, _ := json.Marshal(body)
	sum := sha256.Sum256(canonical)
	result, err := s.store.RecordMovement(r.Context(), transaction, key, hex.EncodeToString(sum[:]))
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrInsufficientStock):
			writeError(w, 409, err.Error())
		case errors.Is(err, storage.ErrNotFound), errors.Is(err, models.ErrPackaging), errors.Is(err, storage.ErrCurrency), errors.Is(err, storage.ErrInvalidMovement):
			writeError(w, 400, err.Error())
		default:
			s.fail(w, r, "record stock movement", err)
		}
		return
	}
	stored := result.Transaction
	if result.Replayed {
		writeJSON(w, 200, stored)
		return
	}
	if result.Product != nil {
		s.publish(w, r, shopID, ws.EventInventoryUpdated, *result.Product)
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

	if b.Status != "" {
		return models.Transaction{}, errors.New("status is decided by the server; use review actions to change it")
	}
	if b.Confidence != nil {
		scores := []float64{b.Confidence.Overall}
		for _, score := range []*float64{b.Confidence.Product, b.Confidence.Quantity, b.Confidence.Amount} {
			if score != nil {
				scores = append(scores, *score)
			}
		}
		for _, score := range scores {
			if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
				return models.Transaction{}, errors.New("confidence must be between 0 and 1")
			}
		}
	}
	status := deriveStatus(b.Confidence)

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

	if confidence.Product == nil || confidence.Quantity == nil || confidence.Amount == nil {
		return models.StatusReviewRequired
	}
	for _, score := range []float64{confidence.Overall, *confidence.Product, *confidence.Quantity, *confidence.Amount} {
		if score < reviewThreshold {
			return models.StatusReviewRequired
		}
	}
	return models.StatusConfirmed
}

// handleTransactionDetail routes requests to /api/transactions/{id} and /api/transactions/{id}/{action}.
func (s *Server) handleTransactionDetail(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/transactions/")
	parts := strings.Split(rel, "/")
	if len(parts) == 1 && parts[0] != "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.getTransaction(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" {
		id := parts[0]
		action := parts[1]
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		switch action {
		case "confirm":
			s.confirmTransaction(w, r, id)
			return
		case "reject":
			s.rejectTransaction(w, r, id)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) getTransaction(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	t, err := s.store.GetTransactionByID(r.Context(), shopID, id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "transaction not found")
		return
	}
	if err != nil {
		s.fail(w, r, "get transaction", err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type confirmTransactionRequest struct {
	ProductID string        `json:"product_id"`
	Quantity  int           `json:"quantity"`
	Unit      models.Unit   `json:"unit"`
	Amount    *models.Money `json:"amount"`
}

func (s *Server) confirmTransaction(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var corrections *storage.ReviewCorrections
	if r.Body != nil && r.ContentLength > 0 {
		var req confirmTransactionRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		corrections = &storage.ReviewCorrections{
			ProductID: strings.TrimSpace(req.ProductID),
			Quantity:  req.Quantity,
			Unit:      req.Unit,
			Amount:    req.Amount,
		}
	}

	result, err := s.store.ResolveReview(r.Context(), shopID, id, storage.ReviewActionConfirm, corrections)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusNotFound, "transaction not found")
		case errors.Is(err, storage.ErrConflict):
			writeError(w, http.StatusConflict, "transaction is not awaiting review")
		case errors.Is(err, storage.ErrInsufficientStock):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, storage.ErrCurrency), errors.Is(err, models.ErrPackaging), errors.Is(err, storage.ErrInvalidMovement):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.fail(w, r, "confirm transaction", err)
		}
		return
	}

	if result.Product != nil {
		s.publish(w, r, shopID, ws.EventInventoryUpdated, *result.Product)
	}
	s.publish(w, r, shopID, ws.EventTransactionUpdated, result.Transaction)
	writeJSON(w, http.StatusOK, result.Transaction)
}

func (s *Server) rejectTransaction(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	result, err := s.store.ResolveReview(r.Context(), shopID, id, storage.ReviewActionReject, nil)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusNotFound, "transaction not found")
		case errors.Is(err, storage.ErrConflict):
			writeError(w, http.StatusConflict, "transaction is not awaiting review")
		case errors.Is(err, storage.ErrInvalidMovement):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.fail(w, r, "reject transaction", err)
		}
		return
	}

	s.publish(w, r, shopID, ws.EventTransactionUpdated, result.Transaction)
	writeJSON(w, http.StatusOK, result.Transaction)
}
