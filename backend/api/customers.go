package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

type createCustomerRequest struct {
	Name                   string        `json:"name"`
	Phone                  string        `json:"phone"`
	Notes                  string        `json:"notes"`
	InitialDebt            *models.Money `json:"initial_debt,omitempty"`
	InitialDebtDescription string        `json:"initial_debt_description,omitempty"`
}

type updateCustomerRequest struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Notes string `json:"notes"`
}

type customersListResponse struct {
	ShopID      string            `json:"shop_id"`
	Customers   []models.Customer `json:"customers"`
	Count       int               `json:"count"`
	Total       int               `json:"total"`
	Limit       int               `json:"limit"`
	Offset      int               `json:"offset"`
	GeneratedAt time.Time         `json:"generated_at"`
}

type customerDebtsResponse struct {
	ShopID      string                `json:"shop_id"`
	CustomerID  string                `json:"customer_id"`
	Debts       []models.CustomerDebt `json:"debts"`
	Count       int                   `json:"count"`
	Total       int                   `json:"total"`
	Limit       int                   `json:"limit"`
	Offset      int                   `json:"offset"`
	GeneratedAt time.Time             `json:"generated_at"`
}

type recordDebtRequest struct {
	Type        models.CustomerDebtType `json:"type"`
	Amount      models.Money            `json:"amount"`
	Description string                  `json:"description"`
	ReceiptRef  string                  `json:"receipt_ref,omitempty"`
	OccurredAt  string                  `json:"occurred_at,omitempty"`
}

// handleCustomers serves GET and POST /api/customers.
func (s *Server) handleCustomers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListCustomers(w, r)
	case http.MethodPost:
		s.handleCreateCustomer(w, r)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

// handleListCustomers retrieves a filtered and paginated list of customers.
func (s *Server) handleListCustomers(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	q := storage.CustomerQuery{
		ShopID: shopID,
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
	}

	hasDebtParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("has_debt")))
	if hasDebtParam == "true" || hasDebtParam == "1" {
		q.HasDebtOnly = true
	}

	if l := r.URL.Query().Get("limit"); l != "" {
		if limit, err := strconv.Atoi(l); err == nil && limit > 0 {
			q.Limit = limit
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if offset, err := strconv.Atoi(o); err == nil && offset >= 0 {
			q.Offset = offset
		}
	}

	customers, total, err := s.store.ListCustomers(r.Context(), q)
	if err != nil {
		s.fail(w, r, "list customers", err)
		return
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}

	writeJSON(w, http.StatusOK, customersListResponse{
		ShopID:      shopID,
		Customers:   customers,
		Count:       len(customers),
		Total:       total,
		Limit:       limit,
		Offset:      q.Offset,
		GeneratedAt: time.Now().UTC(),
	})
}

// handleCreateCustomer registers a new customer, optionally creating an initial credit debt entry.
func (s *Server) handleCreateCustomer(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req createCustomerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "customer name is required")
		return
	}
	if len(name) > 150 {
		writeError(w, http.StatusBadRequest, "customer name exceeds 150 characters")
		return
	}

	phone := strings.TrimSpace(req.Phone)
	if len(phone) > 32 {
		writeError(w, http.StatusBadRequest, "customer phone exceeds 32 characters")
		return
	}

	notes := strings.TrimSpace(req.Notes)
	if len(notes) > 500 {
		writeError(w, http.StatusBadRequest, "customer notes exceed 500 characters")
		return
	}

	shop, err := s.store.GetShop(r.Context(), shopID)
	if err != nil {
		s.fail(w, r, "lookup shop currency for customer", err)
		return
	}
	currency := shop.Currency
	if currency == "" {
		currency = models.CurrencyNGN
	}

	id, err := newID("cus")
	if err != nil {
		s.fail(w, r, "generate customer id", err)
		return
	}

	now := time.Now().UTC()
	customer := models.Customer{
		ID:                 id,
		ShopID:             shopID,
		Name:               name,
		Phone:              phone,
		Notes:              notes,
		OutstandingBalance: models.Money{Amount: 0, Currency: currency},
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := s.store.CreateCustomer(r.Context(), customer); err != nil {
		s.fail(w, r, "save customer", err)
		return
	}

	s.publish(w, r, shopID, ws.EventCustomerCreated, customer)

	// If initial credit sale is specified
	if req.InitialDebt != nil && req.InitialDebt.Amount > 0 {
		debtCurrency := strings.TrimSpace(strings.ToUpper(req.InitialDebt.Currency))
		if debtCurrency == "" {
			debtCurrency = currency
		}

		desc := strings.TrimSpace(req.InitialDebtDescription)
		if desc == "" {
			desc = "Opening balance"
		}

		debtID, err := newID("dbt")
		if err != nil {
			s.fail(w, r, "generate initial debt id", err)
			return
		}

		debt := models.CustomerDebt{
			ID:          debtID,
			ShopID:      shopID,
			CustomerID:  id,
			Type:        models.DebtTypeCreditSale,
			Amount:      models.Money{Amount: req.InitialDebt.Amount, Currency: debtCurrency},
			Description: desc,
			OccurredAt:  now,
			CreatedBy:   "owner",
			CreatedAt:   now,
		}

		recordedDebt, err := s.store.RecordCustomerDebt(r.Context(), debt)
		if err != nil {
			s.fail(w, r, "record initial debt", err)
			return
		}

		updatedCustomer, err := s.store.GetCustomerByID(r.Context(), shopID, id)
		if err != nil {
			s.fail(w, r, "fetch updated customer after initial debt", err)
			return
		}

		s.publish(w, r, shopID, ws.EventCustomerDebtRecorded, map[string]any{
			"debt":     recordedDebt,
			"customer": updatedCustomer,
		})
		customer = updatedCustomer
	}

	writeJSON(w, http.StatusCreated, customer)
}

// handleCustomerCreditSummary returns overall accounts receivable metrics for the shop.
func (s *Server) handleCustomerCreditSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	todayEnd := todayStart.Add(24*time.Hour - time.Nanosecond)

	summary, err := s.store.GetShopCreditSummary(r.Context(), shopID, todayStart, todayEnd)
	if err != nil {
		s.fail(w, r, "get customer credit summary", err)
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

// handleCustomerDetail handles /api/customers/{id} and /api/customers/{id}/debts.
func (s *Server) handleCustomerDetail(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	sub := strings.TrimPrefix(r.URL.Path, "/api/customers/")
	sub = strings.Trim(sub, "/")
	parts := strings.Split(sub, "/")

	if len(parts) == 1 && parts[0] != "" {
		customerID := parts[0]
		switch r.Method {
		case http.MethodGet:
			s.handleGetCustomer(w, r, shopID, customerID)
		case http.MethodPut:
			s.handleUpdateCustomer(w, r, shopID, customerID)
		default:
			methodNotAllowed(w, "GET, PUT")
		}
		return
	}

	if len(parts) == 2 && parts[0] != "" && parts[1] == "debts" {
		customerID := parts[0]
		switch r.Method {
		case http.MethodGet:
			s.handleListCustomerDebts(w, r, shopID, customerID)
		case http.MethodPost:
			s.handleRecordCustomerDebt(w, r, shopID, customerID)
		default:
			methodNotAllowed(w, "GET, POST")
		}
		return
	}

	writeError(w, http.StatusNotFound, "endpoint not found")
}

func (s *Server) handleGetCustomer(w http.ResponseWriter, r *http.Request, shopID, customerID string) {
	customer, err := s.store.GetCustomerByID(r.Context(), shopID, customerID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "customer not found")
		return
	}
	if err != nil {
		s.fail(w, r, "get customer", err)
		return
	}

	writeJSON(w, http.StatusOK, customer)
}

func (s *Server) handleUpdateCustomer(w http.ResponseWriter, r *http.Request, shopID, customerID string) {
	var req updateCustomerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "customer name is required")
		return
	}
	if len(name) > 150 {
		writeError(w, http.StatusBadRequest, "customer name exceeds 150 characters")
		return
	}

	phone := strings.TrimSpace(req.Phone)
	if len(phone) > 32 {
		writeError(w, http.StatusBadRequest, "customer phone exceeds 32 characters")
		return
	}

	notes := strings.TrimSpace(req.Notes)
	if len(notes) > 500 {
		writeError(w, http.StatusBadRequest, "customer notes exceed 500 characters")
		return
	}

	customer, err := s.store.GetCustomerByID(r.Context(), shopID, customerID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "customer not found")
		return
	}
	if err != nil {
		s.fail(w, r, "fetch customer for update", err)
		return
	}

	customer.Name = name
	customer.Phone = phone
	customer.Notes = notes

	err = s.store.UpdateCustomer(r.Context(), shopID, customer)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "customer not found")
		return
	}
	if err != nil {
		s.fail(w, r, "update customer", err)
		return
	}

	updatedCustomer, err := s.store.GetCustomerByID(r.Context(), shopID, customerID)
	if err != nil {
		s.fail(w, r, "fetch updated customer", err)
		return
	}

	writeJSON(w, http.StatusOK, updatedCustomer)
}

func (s *Server) handleListCustomerDebts(w http.ResponseWriter, r *http.Request, shopID, customerID string) {
	// Verify customer exists and belongs to shop
	_, err := s.store.GetCustomerByID(r.Context(), shopID, customerID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "customer not found")
		return
	}
	if err != nil {
		s.fail(w, r, "verify customer", err)
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 200 {
		limit = 200
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	debts, total, err := s.store.ListCustomerDebts(r.Context(), shopID, customerID, limit, offset)
	if err != nil {
		s.fail(w, r, "list customer debts", err)
		return
	}

	// Filter by type in-memory if query parameter is set
	if t := r.URL.Query().Get("type"); t != "" {
		debtType := models.CustomerDebtType(t)
		filtered := make([]models.CustomerDebt, 0, len(debts))
		for _, d := range debts {
			if d.Type == debtType {
				filtered = append(filtered, d)
			}
		}
		debts = filtered
	}

	writeJSON(w, http.StatusOK, customerDebtsResponse{
		ShopID:      shopID,
		CustomerID:  customerID,
		Debts:       debts,
		Count:       len(debts),
		Total:       total,
		Limit:       limit,
		Offset:      offset,
		GeneratedAt: time.Now().UTC(),
	})
}

func (s *Server) handleRecordCustomerDebt(w http.ResponseWriter, r *http.Request, shopID, customerID string) {
	var req recordDebtRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if !req.Type.Valid() {
		writeError(w, http.StatusBadRequest, "invalid debt type: must be credit_sale, repayment, or forgiveness")
		return
	}
	if req.Amount.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "debt amount must be greater than zero")
		return
	}

	currency := strings.TrimSpace(strings.ToUpper(req.Amount.Currency))
	if currency == "" {
		shop, err := s.store.GetShop(r.Context(), shopID)
		if err != nil {
			s.fail(w, r, "lookup shop currency for debt", err)
			return
		}
		currency = shop.Currency
		if currency == "" {
			currency = models.CurrencyNGN
		}
	} else if len(currency) != 3 {
		writeError(w, http.StatusBadRequest, "debt currency must be 3-letter code")
		return
	}

	desc := strings.TrimSpace(req.Description)
	if desc == "" {
		writeError(w, http.StatusBadRequest, "debt description is required")
		return
	}
	if len(desc) > 500 {
		writeError(w, http.StatusBadRequest, "debt description exceeds 500 characters")
		return
	}

	now := time.Now().UTC()
	occurredAt := now
	if req.OccurredAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.OccurredAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "occurred_at must be RFC3339 format")
			return
		}
		if parsed.After(now.Add(24 * time.Hour)) {
			writeError(w, http.StatusBadRequest, "occurred_at cannot be more than 24 hours in future")
			return
		}
		occurredAt = parsed.UTC()
	}

	debtID, err := newID("dbt")
	if err != nil {
		s.fail(w, r, "generate debt id", err)
		return
	}

	debt := models.CustomerDebt{
		ID:          debtID,
		ShopID:      shopID,
		CustomerID:  customerID,
		Type:        req.Type,
		Amount:      models.Money{Amount: req.Amount.Amount, Currency: currency},
		Description: desc,
		ReceiptRef:  strings.TrimSpace(req.ReceiptRef),
		OccurredAt:  occurredAt,
		CreatedBy:   "owner",
		CreatedAt:   now,
	}

	recordedDebt, err := s.store.RecordCustomerDebt(r.Context(), debt)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "customer not found")
		return
	}
	if err != nil {
		s.fail(w, r, "record customer debt", err)
		return
	}

	updatedCustomer, err := s.store.GetCustomerByID(r.Context(), shopID, customerID)
	if err != nil {
		s.fail(w, r, "fetch updated customer after debt", err)
		return
	}

	s.publish(w, r, shopID, ws.EventCustomerDebtRecorded, map[string]any{
		"debt":     recordedDebt,
		"customer": updatedCustomer,
	})

	writeJSON(w, http.StatusCreated, map[string]any{
		"debt":     recordedDebt,
		"customer": updatedCustomer,
	})
}
