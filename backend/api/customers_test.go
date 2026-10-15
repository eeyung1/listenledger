package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

type customerFixture struct {
	Store   *storage.Store
	Handler http.Handler
	ShopID  string
	Cookie  *http.Cookie
}

func setupCustomerFixture(t *testing.T, email, shopName string) *customerFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "customer_api_test.db")
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

	return &customerFixture{
		Store:   store,
		Handler: mux,
		ShopID:  sessionResp.ShopID,
		Cookie:  sessionCookie,
	}
}

func (f *customerFixture) makeRequest(method, path string, body []byte) *httptest.ResponseRecorder {
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

func TestCustomersAPI_AuthenticationAndCSRF(t *testing.T) {
	f := setupCustomerFixture(t, "owner@test.com", "Test Shop")
	unauthFixture := &customerFixture{Store: f.Store, Handler: f.Handler}

	// 1. Unauthenticated GET /api/customers -> 401
	rec := unauthFixture.makeRequest(http.MethodGet, "/api/customers", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauth GET, got %d", rec.Code)
	}

	// 2. Unauthenticated POST /api/customers -> 401
	rec = unauthFixture.makeRequest(http.MethodPost, "/api/customers", []byte(`{"name":"Musa"}`))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauth POST, got %d", rec.Code)
	}

	// 3. Authenticated but missing CSRF header on POST
	req := httptest.NewRequest(http.MethodPost, "/api/customers", bytes.NewBufferString(`{"name":"Musa"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(f.Cookie)
	// Notice no X-ListenLedger-Request header
	rec = httptest.NewRecorder()
	f.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for missing CSRF header, got %d", rec.Code)
	}
}

func TestCustomersAPI_CreateAndRetrieve(t *testing.T) {
	f := setupCustomerFixture(t, "shopkeeper@test.com", "Alaba Electronics")

	// 1. Validation failure: empty name
	rec := f.makeRequest(http.MethodPost, "/api/customers", []byte(`{"name": "   ", "phone": "080123"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Success: create customer without initial debt
	createBody := `{"name": "Emeka Okonkwo", "phone": "08011112222", "notes": "Wholesale buyer"}`
	rec = f.makeRequest(http.MethodPost, "/api/customers", []byte(createBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for customer create, got %d: %s", rec.Code, rec.Body.String())
	}

	var created models.Customer
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode customer: %v", err)
	}

	if created.ID == "" || created.Name != "Emeka Okonkwo" {
		t.Errorf("unexpected created customer: %+v", created)
	}
	if created.OutstandingBalance.Amount != 0 {
		t.Errorf("expected zero balance, got %d", created.OutstandingBalance.Amount)
	}

	// 3. Retrieve single customer
	rec = f.makeRequest(http.MethodGet, "/api/customers/"+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for get customer, got %d: %s", rec.Code, rec.Body.String())
	}
	var fetched models.Customer
	if err := json.NewDecoder(rec.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched customer: %v", err)
	}
	if fetched.ID != created.ID || fetched.Name != "Emeka Okonkwo" {
		t.Errorf("unexpected fetched customer: %+v", fetched)
	}

	// 4. Update customer
	updateBody := `{"name": "Emeka O. Okonkwo", "phone": "08011113333", "notes": "Trusted wholesale customer"}`
	rec = f.makeRequest(http.MethodPut, "/api/customers/"+created.ID, []byte(updateBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for put customer, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated models.Customer
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated customer: %v", err)
	}
	if updated.Name != "Emeka O. Okonkwo" || updated.Phone != "08011113333" {
		t.Errorf("unexpected updated customer fields: %+v", updated)
	}
}

func TestCustomersAPI_CreateWithInitialDebt(t *testing.T) {
	f := setupCustomerFixture(t, "market@test.com", "Balogun Store")

	createBody := `{
		"name": "Mama Ngozi",
		"phone": "08099998888",
		"notes": "Stall 12 neighbour",
		"initial_debt": {
			"amount": 2500000,
			"currency": "NGN"
		},
		"initial_debt_description": "Carried over from paper ledger"
	}`
	rec := f.makeRequest(http.MethodPost, "/api/customers", []byte(createBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for customer create with initial debt, got %d: %s", rec.Code, rec.Body.String())
	}

	var created models.Customer
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode customer: %v", err)
	}

	if created.OutstandingBalance.Amount != 2500000 {
		t.Fatalf("expected 2500000 balance, got %d", created.OutstandingBalance.Amount)
	}

	// Check customer debt line exists
	rec = f.makeRequest(http.MethodGet, "/api/customers/"+created.ID+"/debts", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for debts list, got %d: %s", rec.Code, rec.Body.String())
	}
	var debtsResp struct {
		Debts []models.CustomerDebt `json:"debts"`
		Total int                   `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&debtsResp); err != nil {
		t.Fatalf("decode debts: %v", err)
	}
	if debtsResp.Total != 1 || len(debtsResp.Debts) != 1 {
		t.Fatalf("expected 1 initial debt record, got total %d, len %d", debtsResp.Total, len(debtsResp.Debts))
	}
	if debtsResp.Debts[0].Amount.Amount != 2500000 || debtsResp.Debts[0].Type != models.DebtTypeCreditSale {
		t.Errorf("unexpected initial debt entry: %+v", debtsResp.Debts[0])
	}
}

func TestCustomersAPI_RecordDebtLifecycleAndSummary(t *testing.T) {
	f := setupCustomerFixture(t, "trader@test.com", "Oshodi Supermarket")

	// 1. Create customer
	rec := f.makeRequest(http.MethodPost, "/api/customers", []byte(`{"name": "Baba Tunde"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201: %s", rec.Body.String())
	}
	var customer models.Customer
	_ = json.NewDecoder(rec.Body).Decode(&customer)

	// 2. Record credit sale: NGN 15,000 (1500000 kobo)
	debtBody := `{
		"type": "credit_sale",
		"amount": {"amount": 1500000, "currency": "NGN"},
		"description": "5 cartons of indomie on credit"
	}`
	rec = f.makeRequest(http.MethodPost, "/api/customers/"+customer.ID+"/debts", []byte(debtBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for credit sale: %s", rec.Body.String())
	}
	var saleResult struct {
		Debt     models.CustomerDebt `json:"debt"`
		Customer models.Customer     `json:"customer"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&saleResult); err != nil {
		t.Fatalf("decode sale result: %v", err)
	}
	if saleResult.Customer.OutstandingBalance.Amount != 1500000 {
		t.Errorf("expected customer balance 1500000, got %d", saleResult.Customer.OutstandingBalance.Amount)
	}
	if saleResult.Debt.BalanceAfter.Amount != 1500000 {
		t.Errorf("expected debt balance_after 1500000, got %d", saleResult.Debt.BalanceAfter.Amount)
	}

	// 3. Record partial repayment: NGN 5,000 (500000 kobo)
	repayBody := `{
		"type": "repayment",
		"amount": {"amount": 500000, "currency": "NGN"},
		"description": "Part payment cash"
	}`
	rec = f.makeRequest(http.MethodPost, "/api/customers/"+customer.ID+"/debts", []byte(repayBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for repayment: %s", rec.Body.String())
	}
	var repayResult struct {
		Debt     models.CustomerDebt `json:"debt"`
		Customer models.Customer     `json:"customer"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&repayResult)
	if repayResult.Customer.OutstandingBalance.Amount != 1000000 {
		t.Errorf("expected customer balance 1000000 after repayment, got %d", repayResult.Customer.OutstandingBalance.Amount)
	}

	// 4. Record forgiveness: NGN 2,000 (200000 kobo)
	forgiveBody := `{
		"type": "forgiveness",
		"amount": {"amount": 200000, "currency": "NGN"},
		"description": "Customer loyalty discount"
	}`
	rec = f.makeRequest(http.MethodPost, "/api/customers/"+customer.ID+"/debts", []byte(forgiveBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for forgiveness: %s", rec.Body.String())
	}
	var forgiveResult struct {
		Debt     models.CustomerDebt `json:"debt"`
		Customer models.Customer     `json:"customer"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&forgiveResult)
	if forgiveResult.Customer.OutstandingBalance.Amount != 800000 {
		t.Errorf("expected customer balance 800000 after forgiveness, got %d", forgiveResult.Customer.OutstandingBalance.Amount)
	}

	// 5. Check Credit Summary
	rec = f.makeRequest(http.MethodGet, "/api/customers/credit-summary", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for credit summary: %s", rec.Body.String())
	}
	var summary storage.ShopCreditSummary
	if err := json.NewDecoder(rec.Body).Decode(&summary); err != nil {
		t.Fatalf("decode credit summary: %v", err)
	}
	if summary.TotalCustomersWithDebt != 1 {
		t.Errorf("expected 1 customer with debt, got %d", summary.TotalCustomersWithDebt)
	}
	if summary.TotalOutstandingDebt.Amount != 800000 {
		t.Errorf("expected 800000 outstanding debt, got %d", summary.TotalOutstandingDebt.Amount)
	}
	if summary.TodayCreditSalesAmount.Amount != 1500000 {
		t.Errorf("expected 1500000 today credit sales, got %d", summary.TodayCreditSalesAmount.Amount)
	}
	if summary.TodayRepaymentsAmount.Amount != 500000 {
		t.Errorf("expected 500000 today repayments, got %d", summary.TodayRepaymentsAmount.Amount)
	}
}

func TestCustomersAPI_CrossShopTenantIsolation(t *testing.T) {
	f1 := setupCustomerFixture(t, "shop1@test.com", "Shop Alpha")
	f2 := setupCustomerFixture(t, "shop2@test.com", "Shop Beta")

	// Shop 1 creates customer
	rec := f1.makeRequest(http.MethodPost, "/api/customers", []byte(`{"name": "Alpha Customer"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create failed: %s", rec.Body.String())
	}
	var c1 models.Customer
	_ = json.NewDecoder(rec.Body).Decode(&c1)

	// Shop 2 cannot view Shop 1's customer
	rec = f2.makeRequest(http.MethodGet, "/api/customers/"+c1.ID, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-shop get, got %d", rec.Code)
	}

	// Shop 2 cannot update Shop 1's customer
	rec = f2.makeRequest(http.MethodPut, "/api/customers/"+c1.ID, []byte(`{"name": "Hacked Customer"}`))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-shop put, got %d", rec.Code)
	}

	// Shop 2 cannot record debts against Shop 1's customer
	rec = f2.makeRequest(http.MethodPost, "/api/customers/"+c1.ID+"/debts", []byte(`{
		"type": "credit_sale",
		"amount": {"amount": 50000, "currency": "NGN"},
		"description": "Cross shop debt injection"
	}`))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-shop debt record, got %d", rec.Code)
	}

	// Shop 2 listing customers does not contain Shop 1's customer
	rec = f2.makeRequest(http.MethodGet, "/api/customers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list failed: %s", rec.Body.String())
	}
	var listResp struct {
		Customers []models.Customer `json:"customers"`
		Total     int               `json:"total"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&listResp)
	if listResp.Total != 0 || len(listResp.Customers) != 0 {
		t.Errorf("expected 0 customers for shop 2, got %d", listResp.Total)
	}
}
