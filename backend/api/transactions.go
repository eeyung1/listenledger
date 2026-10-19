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

	"github.com/eeyung1/listenledger-backend/auth"
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

	if receiptID := strings.TrimSpace(q.Get("receipt_id")); receiptID != "" {
		filter.ReceiptID = receiptID
	}

	if customerID := strings.TrimSpace(q.Get("customer_id")); customerID != "" {
		filter.CustomerID = customerID
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
		LastSeq:      s.lastSeq(shopID),
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

	if body.ProductID != "" {
		prod, err := s.store.GetProductByID(r.Context(), shopID, body.ProductID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "product_id does not exist in shop catalogue")
				return
			}
			s.fail(w, r, "lookup product for transaction", err)
			return
		}
		if prod.ArchivedAt != nil {
			writeError(w, http.StatusBadRequest, "product is archived and cannot receive new transactions")
			return
		}
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

// isAlpha checks if string contains only ASCII alphabetic characters.
func isAlpha(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
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
	if b.Quantity > 100000 {
		return models.Transaction{}, errors.New("quantity exceeds maximum allowable threshold (100,000)")
	}

	if !b.Unit.Valid() {
		return models.Transaction{}, errors.New("unit is not a recognised unit")
	}

	currency := strings.TrimSpace(strings.ToUpper(b.Amount.Currency))
	if currency == "" {
		return models.Transaction{}, errors.New("amount.currency is required")
	}
	if len(currency) != 3 || !isAlpha(currency) {
		return models.Transaction{}, errors.New("amount.currency must be a valid 3-letter currency code")
	}
	if b.Amount.Amount < 0 {
		return models.Transaction{}, errors.New("amount.amount cannot be negative")
	}
	if b.Amount.Amount > 1000000000000 { // 10 billion minor units (e.g. 10B naira)
		return models.Transaction{}, errors.New("amount.amount exceeds maximum allowable value")
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
		if action == "decision" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			s.getTransactionDecision(w, r, id)
			return
		}
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
		case "reverse":
			s.reverseTransaction(w, r, id)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) callerIdentity(r *http.Request) string {
	if cookie, err := r.Cookie(auth.CookieName); err == nil && cookie != nil && cookie.Value != "" {
		sum := sha256.Sum256([]byte(cookie.Value))
		ident, err := s.store.SessionIdentity(r.Context(), hex.EncodeToString(sum[:]))
		if err == nil {
			if ident.Email != "" {
				return ident.Email
			}
			if ident.OwnerID != "" {
				return ident.OwnerID
			}
		}
	}
	return "owner"
}

func (s *Server) getTransactionDecision(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	dec, err := s.store.GetReviewDecision(r.Context(), shopID, id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "review decision not found")
		return
	}
	if err != nil {
		s.fail(w, r, "get review decision", err)
		return
	}
	writeJSON(w, http.StatusOK, dec)
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

type createProductOnboard struct {
	Name              string                     `json:"name"`
	SKU               string                     `json:"sku,omitempty"`
	Aliases           []string                   `json:"aliases,omitempty"`
	Price             models.Money               `json:"price"`
	BaseUnit          models.Unit                `json:"base_unit"`
	Packaging         []models.PackagingRelation `json:"packaging,omitempty"`
	LowStockThreshold int                        `json:"low_stock_threshold,omitempty"`
}

type confirmTransactionRequest struct {
	ProductID     string                `json:"product_id,omitempty"`
	Quantity      int                   `json:"quantity,omitempty"`
	Unit          models.Unit           `json:"unit,omitempty"`
	Amount        *models.Money         `json:"amount,omitempty"`
	CustomerID    string                `json:"customer_id,omitempty"`
	CustomerName  string                `json:"customer_name,omitempty"`
	Notes         string                `json:"notes,omitempty"`
	CreateProduct *createProductOnboard `json:"create_product,omitempty"`
}

func (s *Server) confirmTransaction(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var corrections *storage.ReviewCorrections
	notes := ""
	var createProdReq *createProductOnboard

	if r.Body != nil && r.ContentLength > 0 {
		var req confirmTransactionRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		notes = strings.TrimSpace(req.Notes)
		createProdReq = req.CreateProduct

		if req.CreateProduct != nil {
			origTxn, err := s.store.GetTransactionByID(r.Context(), shopID, id)
			if err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					writeError(w, http.StatusNotFound, "transaction not found")
					return
				}
				s.fail(w, r, "lookup transaction for onboarding", err)
				return
			}
			cp := req.CreateProduct
			prodName := strings.TrimSpace(cp.Name)
			if prodName == "" {
				prodName = strings.TrimSpace(origTxn.ProductName)
			}
			if prodName == "" {
				writeError(w, http.StatusBadRequest, "product name is required for catalogue onboarding")
				return
			}
			sku := strings.TrimSpace(cp.SKU)
			if sku == "" {
				sku = generateSKU(prodName)
			}
			baseUnit := cp.BaseUnit
			if !baseUnit.Valid() {
				if req.Unit.Valid() {
					baseUnit = req.Unit
				} else if origTxn.Unit.Valid() {
					baseUnit = origTxn.Unit
				} else {
					baseUnit = models.Unit("piece")
				}
			}
			price := cp.Price
			if price.Currency == "" {
				price.Currency = origTxn.Amount.Currency
			}
			if price.Currency == "" {
				price.Currency = "NGN"
			}
			if price.Amount < 0 {
				writeError(w, http.StatusBadRequest, "price cannot be negative")
				return
			}
			prodID, err := newID("prd")
			if err != nil {
				s.fail(w, r, "generate product id", err)
				return
			}

			if err := models.ValidatePackaging(baseUnit, cp.Packaging); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}

			newProduct := models.Product{
				ID:                prodID,
				ShopID:            shopID,
				Name:              prodName,
				SKU:               sku,
				Aliases:           cp.Aliases,
				Price:             price,
				Quantity:          0,
				BaseUnit:          baseUnit,
				Packaging:         cp.Packaging,
				LowStockThreshold: cp.LowStockThreshold,
			}
			corrections = &storage.ReviewCorrections{
				ProductID:  prodID,
				Quantity:   req.Quantity,
				Unit:       req.Unit,
				Amount:     req.Amount,
				NewProduct: &newProduct,
			}
		} else if req.ProductID != "" || req.Quantity > 0 || req.Unit.Valid() || req.Amount != nil {
			if req.ProductID != "" {
				prod, err := s.store.GetProductByID(r.Context(), shopID, strings.TrimSpace(req.ProductID))
				if err != nil {
					if errors.Is(err, storage.ErrNotFound) {
						writeError(w, http.StatusBadRequest, "product_id does not exist in shop catalogue")
						return
					}
					s.fail(w, r, "lookup product for confirmation", err)
					return
				}
				if prod.ArchivedAt != nil {
					writeError(w, http.StatusBadRequest, "cannot confirm transaction against an archived product")
					return
				}
			}
			if req.Quantity < 0 || req.Quantity > 100000 {
				writeError(w, http.StatusBadRequest, "corrected quantity must be positive and not exceed 100,000")
				return
			}
			if req.Amount != nil && (req.Amount.Amount < 0 || req.Amount.Amount > 1000000000000) {
				writeError(w, http.StatusBadRequest, "corrected amount must be non-negative and bounded")
				return
			}

			corrections = &storage.ReviewCorrections{
				ProductID: strings.TrimSpace(req.ProductID),
				Quantity:  req.Quantity,
				Unit:      req.Unit,
				Amount:    req.Amount,
			}
		}

		if req.CustomerID != "" {
			cust, err := s.store.GetCustomerByID(r.Context(), shopID, strings.TrimSpace(req.CustomerID))
			if err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					writeError(w, http.StatusBadRequest, "customer_id does not exist in shop credit book")
					return
				}
				s.fail(w, r, "lookup customer for confirmation", err)
				return
			}
			if req.CustomerName == "" {
				req.CustomerName = cust.Name
			}
		}

		if corrections == nil && (req.CustomerID != "" || req.CustomerName != "") {
			corrections = &storage.ReviewCorrections{}
		}
		if corrections != nil && (req.CustomerID != "" || req.CustomerName != "") {
			corrections.CustomerID = strings.TrimSpace(req.CustomerID)
			corrections.CustomerName = strings.TrimSpace(req.CustomerName)
		}
	}

	decidedBy := s.callerIdentity(r)
	result, err := s.store.ResolveReview(r.Context(), shopID, id, storage.ReviewActionConfirm, corrections, decidedBy, notes)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusNotFound, "transaction not found")
		case errors.Is(err, storage.ErrConflict):
			writeError(w, http.StatusConflict, "transaction is not awaiting review or conflict exists")
		case errors.Is(err, storage.ErrInsufficientStock):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, storage.ErrCurrency), errors.Is(err, models.ErrPackaging), errors.Is(err, storage.ErrInvalidMovement):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.fail(w, r, "confirm transaction", err)
		}
		return
	}

	if createProdReq != nil && result.Product != nil {
		s.publish(w, r, shopID, ws.EventProductCreated, *result.Product)
	}
	if result.Product != nil {
		s.publish(w, r, shopID, ws.EventInventoryUpdated, *result.Product)
	}
	s.publish(w, r, shopID, ws.EventTransactionUpdated, result.Transaction)

	// Attribute credit sale to customer's tab if not already recorded
	if result.Transaction.Status == models.StatusConfirmed && result.Transaction.Type == models.TypeSale && result.Transaction.CustomerID != "" {
		lowerTranscript := strings.ToLower(result.Transaction.Transcript)
		isCredit := strings.Contains(lowerTranscript, "credit") || strings.Contains(lowerTranscript, "tab") || strings.Contains(lowerTranscript, "debt") || strings.Contains(lowerTranscript, "account")
		if isCredit {
			debts, _, _ := s.store.ListCustomerDebts(r.Context(), shopID, result.Transaction.CustomerID, 50, 0)
			alreadyRecorded := false
			for _, d := range debts {
				if d.ReceiptRef == result.Transaction.ID || (result.Transaction.ReceiptID != "" && d.ReceiptRef == result.Transaction.ReceiptID) {
					alreadyRecorded = true
					break
				}
			}
			if !alreadyRecorded && result.Transaction.Amount.Amount > 0 {
				debtID := fmt.Sprintf("dbt_rev_%s", result.Transaction.ID)
				debtDesc := fmt.Sprintf("Confirmed credit sale (%s)", result.Transaction.ID)
				recordedDebt, err := s.store.RecordCustomerDebt(r.Context(), models.CustomerDebt{
					ID:          debtID,
					ShopID:      shopID,
					CustomerID:  result.Transaction.CustomerID,
					Type:        models.DebtTypeCreditSale,
					Amount:      result.Transaction.Amount,
					Description: debtDesc,
					ReceiptRef:  result.Transaction.ID,
					OccurredAt:  result.Transaction.OccurredAt,
					CreatedBy:   decidedBy,
				})
				if err == nil {
					updatedCust, err := s.store.GetCustomerByID(r.Context(), shopID, result.Transaction.CustomerID)
					if err == nil {
						s.publish(w, r, shopID, ws.EventCustomerDebtRecorded, map[string]any{
							"debt":     recordedDebt,
							"customer": updatedCust,
						})
					}
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, result.Transaction)
}

func generateSKU(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteRune('-')
			}
		}
	}
	res := strings.Trim(b.String(), "-")
	if len(res) > 16 {
		res = res[:16]
	}
	if res == "" {
		res = "SKU"
	}
	return fmt.Sprintf("%s-%d", res, (time.Now().UnixNano()/1e6)%10000)
}

type rejectTransactionRequest struct {
	Notes string `json:"notes,omitempty"`
}

func (s *Server) rejectTransaction(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	notes := ""
	if r.Body != nil && r.ContentLength > 0 {
		var req rejectTransactionRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		notes = strings.TrimSpace(req.Notes)
	}

	decidedBy := s.callerIdentity(r)
	result, err := s.store.ResolveReview(r.Context(), shopID, id, storage.ReviewActionReject, nil, decidedBy, notes)
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

type reverseTransactionRequest struct {
	Reason string `json:"reason"`
}

type reverseResponse struct {
	Transaction models.Transaction `json:"transaction"`
	Reversal    models.Transaction `json:"reversal"`
	Product     *models.Product    `json:"product,omitempty"`
}

func (s *Server) reverseTransaction(w http.ResponseWriter, r *http.Request, id string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req reverseTransactionRequest
	if r.Body != nil && r.ContentLength > 0 {
		_ = decodeJSON(w, r, &req)
	}

	res, err := s.store.ReverseTransaction(r.Context(), shopID, id, req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusNotFound, "transaction not found")
		case errors.Is(err, storage.ErrConflict):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, storage.ErrInsufficientStock):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, models.ErrPackaging):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.fail(w, r, "reverse transaction", err)
		}
		return
	}

	if res.Product != nil {
		s.publish(w, r, shopID, ws.EventInventoryUpdated, *res.Product)
	}
	s.publish(w, r, shopID, ws.EventTransactionUpdated, res.Original)
	s.publish(w, r, shopID, ws.EventTransactionCreated, res.Reversal)

	writeJSON(w, http.StatusOK, reverseResponse{
		Transaction: res.Original,
		Reversal:    res.Reversal,
		Product:     res.Product,
	})
}

type manualTransactionRequest struct {
	// RequestID is minted by the client once, before the first POST, and reused
	// for every retry (P4.8). It is the idempotency key, so a manual entry that
	// committed but whose response was lost replays instead of being recorded
	// twice. It may also arrive as the Idempotency-Key header for parity with
	// the capture endpoint.
	RequestID string                 `json:"request_id"`
	Type      models.TransactionType `json:"type"`
	ProductID string                 `json:"product_id"`
	Quantity  int                    `json:"quantity"`
	Unit      models.Unit            `json:"unit"`
	Amount    *models.Money          `json:"amount,omitempty"`
	Notes     string                 `json:"notes,omitempty"`
}

// manualRequestFingerprint is the stable, caller-controlled shape of a manual
// entry, used to tell a genuine retry (same id, same content → replay) from a
// conflicting reuse (same id, different content → 409). It deliberately
// excludes server-minted ids, timestamps and catalogue-derived prices: those
// change on every attempt (or when the catalogue is edited) and must not turn a
// harmless retry into a conflict.
type manualRequestFingerprint struct {
	Type      models.TransactionType `json:"type"`
	ProductID string                 `json:"product_id"`
	Quantity  int                    `json:"quantity"`
	Unit      models.Unit            `json:"unit"`
	Amount    *int64                 `json:"amount,omitempty"`
	Notes     string                 `json:"notes"`
}

type manualTransactionResponse struct {
	Transaction models.Transaction `json:"transaction"`
	Product     *models.Product    `json:"product,omitempty"`
}

// manualFingerprint derives the idempotency fingerprint from the caller's
// request fields only. product_id is expected to be trimmed already, and notes
// is the trimmed transcript note the handler computed.
func manualFingerprint(req manualTransactionRequest, notes string) manualRequestFingerprint {
	fp := manualRequestFingerprint{
		Type:      req.Type,
		ProductID: req.ProductID,
		Quantity:  req.Quantity,
		Unit:      req.Unit,
		Notes:     notes,
	}
	if req.Amount != nil {
		amount := req.Amount.Amount
		fp.Amount = &amount
	}
	return fp
}

func (s *Server) handleTransactionsManual(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req manualTransactionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// P4.8: require a stable, client-minted request id before anything else so a
	// caller cannot fall back to the old "mint a fresh key per request" path.
	// A body field and the Idempotency-Key header are both accepted; the body
	// wins when present.
	requestKey := strings.TrimSpace(req.RequestID)
	if requestKey == "" {
		requestKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if requestKey == "" || len(requestKey) > 128 {
		writeError(w, http.StatusBadRequest, "request_id is required (maximum 128 bytes)")
		return
	}

	if !req.Type.Valid() {
		writeError(w, http.StatusBadRequest, "type must be 'sale' or 'restock'")
		return
	}
	if req.Quantity <= 0 {
		writeError(w, http.StatusBadRequest, "quantity must be greater than zero")
		return
	}
	if req.Quantity > 100000 {
		writeError(w, http.StatusBadRequest, "quantity exceeds maximum allowable threshold (100,000)")
		return
	}
	if !req.Unit.Valid() {
		writeError(w, http.StatusBadRequest, "unit is not a recognized packaging unit")
		return
	}
	req.ProductID = strings.TrimSpace(req.ProductID)
	if req.ProductID == "" {
		writeError(w, http.StatusBadRequest, "product_id is required for manual entries")
		return
	}

	product, err := s.store.GetProductByID(r.Context(), shopID, req.ProductID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		s.fail(w, r, "fetch product for manual transaction", err)
		return
	}
	if product.ArchivedAt != nil {
		writeError(w, http.StatusBadRequest, "cannot record manual transaction for an archived product")
		return
	}

	// Calculate or validate amount
	amount := models.Money{Amount: 0, Currency: product.Price.Currency}
	if req.Amount != nil {
		if req.Amount.Amount < 0 || req.Amount.Amount > 1000000000000 {
			writeError(w, http.StatusBadRequest, "amount must be non-negative and bounded")
			return
		}
		if req.Amount.Amount > 0 {
			if req.Amount.Currency != "" && req.Amount.Currency != product.Price.Currency {
				writeError(w, http.StatusBadRequest, "currency mismatch with product currency")
				return
			}
			amount = *req.Amount
			amount.Currency = product.Price.Currency
		}
	} else if req.Type == models.TypeSale && product.Price.Amount > 0 {
		// Default sale amount to catalogue unit price
		amount = models.Money{
			Amount:   int64(req.Quantity) * product.Price.Amount,
			Currency: product.Price.Currency,
		}
	}

	id, err := newID("txn_man")
	if err != nil {
		s.fail(w, r, "generate transaction id", err)
		return
	}

	notes := strings.TrimSpace(req.Notes)
	transcript := "Manual counter entry"
	if notes != "" {
		transcript = fmt.Sprintf("Manual counter entry: %s", notes)
	}

	now := time.Now().UTC()
	txn := models.Transaction{
		ID:          id,
		ShopID:      shopID,
		Type:        req.Type,
		ProductID:   product.ID,
		ProductName: product.Name,
		Quantity:    req.Quantity,
		Unit:        req.Unit,
		Amount:      amount,
		Confidence:  models.Confidence{Overall: 1.0},
		Status:      models.StatusConfirmed,
		OccurredAt:  now,
		CreatedAt:   now,
		Transcript:  transcript,
	}

	idempotencyKey := requestKey
	fingerprint := manualFingerprint(req, notes)
	payloadBytes, _ := json.Marshal(fingerprint)
	sum := sha256.Sum256(payloadBytes)
	hashStr := hex.EncodeToString(sum[:])

	result, err := s.store.RecordMovement(r.Context(), txn, idempotencyKey, hashStr)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrConflict):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, storage.ErrInsufficientStock):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, storage.ErrCurrency), errors.Is(err, models.ErrPackaging), errors.Is(err, storage.ErrInvalidMovement):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			s.fail(w, r, "record manual transaction", err)
		}
		return
	}

	if result.Replayed {
		// A retry of a request that committed but whose response was lost. Return
		// the original transaction without moving stock a second time or
		// re-broadcasting events, so connected dashboards do not double-count it.
		var product *models.Product
		if result.Transaction.ProductID != "" {
			if p, err := s.store.GetProductByID(r.Context(), shopID, result.Transaction.ProductID); err == nil {
				product = &p
			}
		}
		writeJSON(w, http.StatusOK, manualTransactionResponse{
			Transaction: result.Transaction,
			Product:     product,
		})
		return
	}

	if result.Product != nil {
		s.publish(w, r, shopID, ws.EventInventoryUpdated, *result.Product)
	}
	s.publish(w, r, shopID, ws.EventTransactionCreated, result.Transaction)

	writeJSON(w, http.StatusCreated, manualTransactionResponse{
		Transaction: result.Transaction,
		Product:     result.Product,
	})
}

type receiptResponse struct {
	ReceiptID      string               `json:"receipt_id"`
	ShopID         string               `json:"shop_id"`
	Transactions   []models.Transaction `json:"transactions"`
	TotalCount     int                  `json:"total_count"`
	ConfirmedCount int                  `json:"confirmed_count"`
	ReviewCount    int                  `json:"review_count"`
	RejectedCount  int                  `json:"rejected_count"`
	TotalAmount    models.Money         `json:"total_amount"`
}

type resolveReceiptRequest struct {
	Notes string `json:"notes,omitempty"`
}

func (s *Server) handleReceiptDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/receipts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.getReceipt(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" {
		receiptID := parts[0]
		action := parts[1]
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		switch action {
		case "confirm":
			s.confirmReceipt(w, r, receiptID)
			return
		case "reject":
			s.rejectReceipt(w, r, receiptID)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) getReceipt(w http.ResponseWriter, r *http.Request, receiptID string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}
	txns, _, err := s.store.QueryTransactions(r.Context(), shopID, storage.TransactionFilter{
		ReceiptID: receiptID,
		Limit:     100,
	})
	if err != nil {
		s.fail(w, r, "get receipt", err)
		return
	}
	if len(txns) == 0 {
		writeError(w, http.StatusNotFound, "receipt not found")
		return
	}

	var confirmedCount, reviewCount, rejectedCount int
	var totalAmount int64
	currency := txns[0].Amount.Currency
	for _, t := range txns {
		switch t.Status {
		case models.StatusConfirmed:
			confirmedCount++
			if t.Amount.Currency == currency {
				totalAmount += t.Amount.Amount
			}
		case models.StatusReviewRequired:
			reviewCount++
		case models.StatusRejected:
			rejectedCount++
		}
	}

	writeJSON(w, http.StatusOK, receiptResponse{
		ReceiptID:      receiptID,
		ShopID:         shopID,
		Transactions:   txns,
		TotalCount:     len(txns),
		ConfirmedCount: confirmedCount,
		ReviewCount:    reviewCount,
		RejectedCount:  rejectedCount,
		TotalAmount:    models.Money{Amount: totalAmount, Currency: currency},
	})
}

func (s *Server) confirmReceipt(w http.ResponseWriter, r *http.Request, receiptID string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req resolveReceiptRequest
	if r.Body != nil && r.ContentLength > 0 {
		_ = decodeJSON(w, r, &req)
	}

	txns, _, err := s.store.QueryTransactions(r.Context(), shopID, storage.TransactionFilter{
		ReceiptID: receiptID,
		Limit:     100,
	})
	if err != nil {
		s.fail(w, r, "load receipt transactions", err)
		return
	}
	if len(txns) == 0 {
		writeError(w, http.StatusNotFound, "receipt not found")
		return
	}

	decidedBy := s.callerIdentity(r)
	for _, t := range txns {
		if t.Status == models.StatusReviewRequired {
			res, err := s.store.ResolveReview(r.Context(), shopID, t.ID, storage.ReviewActionConfirm, nil, decidedBy, req.Notes)
			if err != nil {
				switch {
				case errors.Is(err, storage.ErrInsufficientStock):
					writeError(w, http.StatusConflict, fmt.Sprintf("line %s: %s", t.ProductName, err.Error()))
				default:
					s.fail(w, r, "confirm receipt line", err)
				}
				return
			}
			if res.Product != nil {
				s.publish(w, r, shopID, ws.EventInventoryUpdated, *res.Product)
			}
			s.publish(w, r, shopID, ws.EventTransactionUpdated, res.Transaction)
		}
	}

	s.getReceipt(w, r, receiptID)
}

func (s *Server) rejectReceipt(w http.ResponseWriter, r *http.Request, receiptID string) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req resolveReceiptRequest
	if r.Body != nil && r.ContentLength > 0 {
		_ = decodeJSON(w, r, &req)
	}

	txns, _, err := s.store.QueryTransactions(r.Context(), shopID, storage.TransactionFilter{
		ReceiptID: receiptID,
		Limit:     100,
	})
	if err != nil {
		s.fail(w, r, "load receipt transactions", err)
		return
	}
	if len(txns) == 0 {
		writeError(w, http.StatusNotFound, "receipt not found")
		return
	}

	decidedBy := s.callerIdentity(r)
	for _, t := range txns {
		if t.Status == models.StatusReviewRequired {
			res, err := s.store.ResolveReview(r.Context(), shopID, t.ID, storage.ReviewActionReject, nil, decidedBy, req.Notes)
			if err != nil {
				s.fail(w, r, "reject receipt line", err)
				return
			}
			s.publish(w, r, shopID, ws.EventTransactionUpdated, res.Transaction)
		}
	}

	s.getReceipt(w, r, receiptID)
}
