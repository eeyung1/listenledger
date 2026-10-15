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

type createExpenseRequest struct {
	Category    models.ExpenseCategory `json:"category"`
	Amount      models.Money           `json:"amount"`
	Description string                 `json:"description"`
	ReceiptRef  string                 `json:"receipt_ref"`
	OccurredAt  string                 `json:"occurred_at"`
}

type expensesListResponse struct {
	ShopID      string           `json:"shop_id"`
	Expenses    []models.Expense `json:"expenses"`
	Count       int              `json:"count"`
	Total       int              `json:"total"`
	Limit       int              `json:"limit"`
	Offset      int              `json:"offset"`
	GeneratedAt time.Time        `json:"generated_at"`
}

// handleExpenses serves GET and POST /api/expenses.
func (s *Server) handleExpenses(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListExpenses(w, r)
	case http.MethodPost:
		s.handleCreateExpense(w, r)
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

// handleListExpenses retrieves a filtered and paginated list of expenses.
func (s *Server) handleListExpenses(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	q := storage.ExpenseQuery{
		ShopID: shopID,
	}

	if cat := r.URL.Query().Get("category"); cat != "" {
		category := models.ExpenseCategory(cat)
		if !category.Valid() {
			writeError(w, http.StatusBadRequest, "invalid expense category filter")
			return
		}
		q.Category = category
	}

	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		from, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from must be RFC3339 timestamp")
			return
		}
		q.From = &from
	}

	if toStr := r.URL.Query().Get("to"); toStr != "" {
		to, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "to must be RFC3339 timestamp")
			return
		}
		q.To = &to
	}

	if l := r.URL.Query().Get("limit"); l != "" {
		if limit, err := strconv.Atoi(l); err == nil {
			q.Limit = limit
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if offset, err := strconv.Atoi(o); err == nil {
			q.Offset = offset
		}
	}

	expenses, total, err := s.store.ListExpenses(r.Context(), q)
	if err != nil {
		s.fail(w, r, "list expenses", err)
		return
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}

	writeJSON(w, http.StatusOK, expensesListResponse{
		ShopID:      shopID,
		Expenses:    expenses,
		Count:       len(expenses),
		Total:       total,
		Limit:       limit,
		Offset:      q.Offset,
		GeneratedAt: time.Now().UTC(),
	})
}

// handleCreateExpense records a new operational expense.
func (s *Server) handleCreateExpense(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	var req createExpenseRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if !req.Category.Valid() {
		writeError(w, http.StatusBadRequest, "invalid expense category")
		return
	}
	if req.Amount.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "expense amount must be greater than zero")
		return
	}

	currency := strings.TrimSpace(strings.ToUpper(req.Amount.Currency))
	if currency == "" {
		// Lookup shop currency default
		shop, err := s.store.GetShop(r.Context(), shopID)
		if err != nil {
			s.fail(w, r, "lookup shop currency for expense", err)
			return
		}
		currency = shop.Currency
		if currency == "" {
			currency = models.CurrencyNGN
		}
	} else if len(currency) != 3 {
		writeError(w, http.StatusBadRequest, "expense currency must be 3-letter code")
		return
	}

	desc := strings.TrimSpace(req.Description)
	if desc == "" {
		writeError(w, http.StatusBadRequest, "expense description is required")
		return
	}
	if len(desc) > 500 {
		writeError(w, http.StatusBadRequest, "expense description exceeds 500 characters")
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

	id, err := newID("exp")
	if err != nil {
		s.fail(w, r, "generate expense id", err)
		return
	}

	expense := models.Expense{
		ID:          id,
		ShopID:      shopID,
		Category:    req.Category,
		Amount:      models.Money{Amount: req.Amount.Amount, Currency: currency},
		Description: desc,
		ReceiptRef:  strings.TrimSpace(req.ReceiptRef),
		OccurredAt:  occurredAt,
		CreatedBy:   "owner",
		CreatedAt:   now,
	}

	if err := s.store.CreateExpense(r.Context(), expense); err != nil {
		s.fail(w, r, "save expense", err)
		return
	}

	s.publish(w, r, shopID, ws.EventExpenseCreated, expense)
	writeJSON(w, http.StatusCreated, expense)
}

// handleExpenseDetail serves GET and DELETE /api/expenses/{id}.
func (s *Server) handleExpenseDetail(w http.ResponseWriter, r *http.Request) {
	shopID, ok := s.shopID(w, r)
	if !ok {
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/expenses/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "expense not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		expense, err := s.store.GetExpenseByID(r.Context(), shopID, id)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "expense not found")
			return
		}
		if err != nil {
			s.fail(w, r, "get expense", err)
			return
		}
		writeJSON(w, http.StatusOK, expense)

	case http.MethodDelete:
		err := s.store.DeleteExpense(r.Context(), shopID, id)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "expense not found")
			return
		}
		if err != nil {
			s.fail(w, r, "delete expense", err)
			return
		}

		s.publish(w, r, shopID, ws.EventExpenseDeleted, map[string]string{"id": id})
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})

	default:
		methodNotAllowed(w, "GET, DELETE")
	}
}
