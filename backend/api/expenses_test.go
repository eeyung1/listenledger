package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

type expenseFixture struct {
	Store   *storage.Store
	Handler http.Handler
	ShopID  string
	Cookie  *http.Cookie
}

func setupExpenseFixture(t *testing.T, email, shopName string) *expenseFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "expense_api_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	authSvc := &auth.Service{Store: store, SecureCookies: false}
	apiHandler := api.New(store, nil, authSvc.ResolveShop, nil)

	mux := http.NewServeMux()
	mux.Handle("/api/auth/", authSvc.Handler())
	mux.Handle("/api/", auth.ProtectWrites(apiHandler))

	signupBody := fmt.Sprintf(`{"email": %q, "password": "Password123!", "shop_name": %q}`, email, shopName)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(signupBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ListenLedger-Request", "1")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("signup failed with %d: %s", w.Code, w.Body.String())
	}

	var sessionResp struct {
		ShopID string `json:"shop_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&sessionResp); err != nil {
		t.Fatalf("decode signup body: %v", err)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("missing session cookie")
	}

	return &expenseFixture{
		Store:   store,
		Handler: mux,
		ShopID:  sessionResp.ShopID,
		Cookie:  sessionCookie,
	}
}

func (f *expenseFixture) makeRequest(method, path string, body []byte) *httptest.ResponseRecorder {
	var bodyReader *bytes.Buffer
	if body != nil {
		bodyReader = bytes.NewBuffer(body)
	} else {
		bodyReader = bytes.NewBuffer(nil)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ListenLedger-Request", "1")
	if f.Cookie != nil {
		req.AddCookie(f.Cookie)
	}
	rec := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec, req)
	return rec
}

func TestExpensesAPI_AuthenticationRequired(t *testing.T) {
	f := setupExpenseFixture(t, "unauth@test.com", "Test Shop")
	unauthFixture := &expenseFixture{Store: f.Store, Handler: f.Handler}

	// 1. Unauthenticated POST
	rec := unauthFixture.makeRequest(http.MethodPost, "/api/expenses", []byte(`{
		"category": "generator_fuel",
		"amount": {"amount": 500000, "currency": "NGN"},
		"description": "10L fuel"
	}`))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Unauthenticated GET
	rec = unauthFixture.makeRequest(http.MethodGet, "/api/expenses", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestExpensesAPI_CreateAndGetExpense(t *testing.T) {
	f := setupExpenseFixture(t, "mama@test.com", "Mama Tolu Provisions")

	payload := `{
		"category": "generator_fuel",
		"amount": {"amount": 500000},
		"description": "10 litres generator fuel for morning run",
		"receipt_ref": "PETROL-001"
	}`

	rec := f.makeRequest(http.MethodPost, "/api/expenses", []byte(payload))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var created models.Expense
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created expense: %v", err)
	}

	if created.ID == "" || !strings.HasPrefix(created.ID, "exp_") {
		t.Errorf("expected exp_ prefixed ID, got %s", created.ID)
	}
	if created.ShopID != f.ShopID {
		t.Errorf("expected shop_id %s, got %s", f.ShopID, created.ShopID)
	}
	if created.Category != models.ExpenseCategoryGeneratorFuel {
		t.Errorf("expected category generator_fuel, got %s", created.Category)
	}
	if created.Amount.Amount != 500000 || created.Amount.Currency != "NGN" {
		t.Errorf("expected amount 500000 NGN, got %+v", created.Amount)
	}
	if created.Description != "10 litres generator fuel for morning run" {
		t.Errorf("description mismatch: %s", created.Description)
	}
	if created.ReceiptRef != "PETROL-001" {
		t.Errorf("receipt ref mismatch: %s", created.ReceiptRef)
	}

	// Fetch detail by ID
	recGet := f.makeRequest(http.MethodGet, "/api/expenses/"+created.ID, nil)
	if recGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK fetching expense by ID, got %d: %s", recGet.Code, recGet.Body.String())
	}
	var fetched models.Expense
	if err := json.NewDecoder(recGet.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched expense: %v", err)
	}
	if fetched.ID != created.ID {
		t.Errorf("expected fetched ID %s, got %s", created.ID, fetched.ID)
	}
}

func TestExpensesAPI_ValidationFailures(t *testing.T) {
	f := setupExpenseFixture(t, "val@test.com", "Validation Shop")

	tests := []struct {
		name       string
		payload    string
		wantStatus int
		wantError  string
	}{
		{
			name: "invalid category",
			payload: `{
				"category": "personal_vacation",
				"amount": {"amount": 500000, "currency": "NGN"},
				"description": "Trip to Dubai"
			}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid expense category",
		},
		{
			name: "zero amount",
			payload: `{
				"category": "generator_fuel",
				"amount": {"amount": 0, "currency": "NGN"},
				"description": "Free fuel"
			}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "expense amount must be greater than zero",
		},
		{
			name: "negative amount",
			payload: `{
				"category": "generator_fuel",
				"amount": {"amount": -10000, "currency": "NGN"},
				"description": "Negative expense"
			}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "expense amount must be greater than zero",
		},
		{
			name: "empty description",
			payload: `{
				"category": "generator_fuel",
				"amount": {"amount": 500000, "currency": "NGN"},
				"description": "    "
			}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "expense description is required",
		},
		{
			name: "invalid currency length",
			payload: `{
				"category": "generator_fuel",
				"amount": {"amount": 500000, "currency": "NIGERIAN_NAIRA"},
				"description": "Long currency"
			}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "expense currency must be 3-letter code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.makeRequest(http.MethodPost, "/api/expenses", []byte(tt.payload))
			if rec.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantError) {
				t.Errorf("expected error %q in body, got: %s", tt.wantError, rec.Body.String())
			}
		})
	}
}

func TestExpensesAPI_ListFilterAndDelete(t *testing.T) {
	f := setupExpenseFixture(t, "list@test.com", "List Shop")

	// Create 3 expenses
	expFuel := `{"category": "generator_fuel", "amount": {"amount": 400000}, "description": "Petrol"}`
	expRent := `{"category": "store_rent", "amount": {"amount": 2000000}, "description": "Monthly shop stall rent"}`
	expKeke := `{"category": "transport_logistics", "amount": {"amount": 150000}, "description": "Keke delivery"}`

	rec1 := f.makeRequest(http.MethodPost, "/api/expenses", []byte(expFuel))
	rec2 := f.makeRequest(http.MethodPost, "/api/expenses", []byte(expRent))
	rec3 := f.makeRequest(http.MethodPost, "/api/expenses", []byte(expKeke))

	if rec1.Code != 201 || rec2.Code != 201 || rec3.Code != 201 {
		t.Fatalf("failed creating test expenses")
	}

	var createdRent models.Expense
	_ = json.NewDecoder(rec2.Body).Decode(&createdRent)

	// 1. List all
	recList := f.makeRequest(http.MethodGet, "/api/expenses", nil)
	if recList.Code != http.StatusOK {
		t.Fatalf("list expenses failed: %d", recList.Code)
	}
	var listResp struct {
		Expenses []models.Expense `json:"expenses"`
		Count    int              `json:"count"`
		Total    int              `json:"total"`
	}
	if err := json.NewDecoder(recList.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if listResp.Total != 3 || listResp.Count != 3 {
		t.Errorf("expected 3 expenses, got total=%d count=%d", listResp.Total, listResp.Count)
	}

	// 2. Filter by category
	recCat := f.makeRequest(http.MethodGet, "/api/expenses?category=store_rent", nil)
	var catResp struct {
		Expenses []models.Expense `json:"expenses"`
		Total    int              `json:"total"`
	}
	_ = json.NewDecoder(recCat.Body).Decode(&catResp)
	if catResp.Total != 1 || len(catResp.Expenses) != 1 || catResp.Expenses[0].Category != models.ExpenseCategoryStoreRent {
		t.Errorf("expected 1 rent expense, got total=%d len=%d", catResp.Total, len(catResp.Expenses))
	}

	// 3. Delete rent expense
	recDel := f.makeRequest(http.MethodDelete, "/api/expenses/"+createdRent.ID, nil)
	if recDel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK deleting expense, got %d: %s", recDel.Code, recDel.Body.String())
	}

	// 4. Verify count is now 2
	recListAfter := f.makeRequest(http.MethodGet, "/api/expenses", nil)
	var afterResp struct {
		Total int `json:"total"`
	}
	_ = json.NewDecoder(recListAfter.Body).Decode(&afterResp)
	if afterResp.Total != 2 {
		t.Errorf("expected 2 expenses after deletion, got %d", afterResp.Total)
	}

	// 5. Subsequent GET by ID returns 404
	recNotFound := f.makeRequest(http.MethodGet, "/api/expenses/"+createdRent.ID, nil)
	if recNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404 for deleted expense, got %d", recNotFound.Code)
	}
}

func TestExpensesAPI_DailySummaryNetProfitIntegration(t *testing.T) {
	f := setupExpenseFixture(t, "profit@test.com", "Profit Shop")

	// 1. Add product and record sale
	prodPayload := `{
		"name": "Custard 2kg",
		"sku": "CUSTARD-2KG",
		"base_unit": "piece",
		"price": {"amount": 400000, "currency": "NGN"},
		"quantity": 20
	}`
	prodRec := f.makeRequest(http.MethodPost, "/api/products", []byte(prodPayload))
	if prodRec.Code != http.StatusCreated {
		t.Fatalf("create product failed: %d: %s", prodRec.Code, prodRec.Body.String())
	}
	var prod models.Product
	_ = json.NewDecoder(prodRec.Body).Decode(&prod)

	salePayload := fmt.Sprintf(`{
		"type": "sale",
		"product_id": %q,
		"quantity": 2,
		"unit": "piece",
		"amount": {"amount": 800000, "currency": "NGN"}
	}`, prod.ID)
	reqSale := httptest.NewRequest(http.MethodPost, "/api/transactions/manual", bytes.NewBufferString(salePayload))
	reqSale.Header.Set("Content-Type", "application/json")
	reqSale.Header.Set("Idempotency-Key", "manual_sale_profit_01")
	reqSale.Header.Set("X-ListenLedger-Request", "1")
	reqSale.AddCookie(f.Cookie)
	recSale := httptest.NewRecorder()
	f.Handler.ServeHTTP(recSale, reqSale)
	if recSale.Code != http.StatusCreated {
		t.Fatalf("record manual sale failed: %d: %s", recSale.Code, recSale.Body.String())
	}

	// 2. Record operational expense (generator petrol: 300,000 kobo = 3,000 NGN)
	expPayload := `{
		"category": "generator_fuel",
		"amount": {"amount": 300000, "currency": "NGN"},
		"description": "Generator petrol afternoon"
	}`
	recExp := f.makeRequest(http.MethodPost, "/api/expenses", []byte(expPayload))
	if recExp.Code != http.StatusCreated {
		t.Fatalf("record expense failed: %d: %s", recExp.Code, recExp.Body.String())
	}

	// 3. Query GET /api/summary
	recSummary := f.makeRequest(http.MethodGet, "/api/summary", nil)
	if recSummary.Code != http.StatusOK {
		t.Fatalf("get daily summary failed: %d: %s", recSummary.Code, recSummary.Body.String())
	}

	var summary storage.DailySummary
	if err := json.NewDecoder(recSummary.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	if summary.TodaySalesAmount.Amount != 800000 {
		t.Errorf("expected sales 800000, got %d", summary.TodaySalesAmount.Amount)
	}
	if summary.TodaySalesCount != 1 {
		t.Errorf("expected sales count 1, got %d", summary.TodaySalesCount)
	}
	if summary.TodayExpensesAmount.Amount != 300000 {
		t.Errorf("expected expenses 300000, got %d", summary.TodayExpensesAmount.Amount)
	}
	if summary.TodayExpensesCount != 1 {
		t.Errorf("expected expenses count 1, got %d", summary.TodayExpensesCount)
	}
	// Net Profit: 800,000 - 300,000 = 500,000 kobo (5,000 NGN)
	expectedProfit := int64(500000)
	if summary.TodayNetProfit.Amount != expectedProfit {
		t.Errorf("expected net profit %d, got %d", expectedProfit, summary.TodayNetProfit.Amount)
	}
}
