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

type tenantFixture struct {
	Store    *storage.Store
	Handler  http.Handler
	AuthSvc  *auth.Service
	ShopAID  string
	CookieA  *http.Cookie
	ShopBID  string
	CookieB  *http.Cookie
	DeviceA  string
	TokenA   string
	ProductA string
	TxnA     string
}

func setupTenantFixture(t *testing.T) *tenantFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "isolation_test.db")
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

	// 1. Sign up Shop Alpha
	signupA := bytes.NewBufferString(`{
		"email": "alpha_owner@example.com",
		"password": "PasswordAlpha123!",
		"shop_name": "Alpha Supermarket"
	}`)
	reqA := httptest.NewRequest(http.MethodPost, "/api/auth/signup", signupA)
	reqA.Header.Set("Content-Type", "application/json")
	reqA.Header.Set("X-ListenLedger-Request", "1")
	recA := httptest.NewRecorder()
	mux.ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("signup A failed: %d: %s", recA.Code, recA.Body.String())
	}
	cookieA := recA.Result().Cookies()[0]

	// Get Shop A identity
	var identA storage.Identity
	sessReqA := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessReqA.AddCookie(cookieA)
	sessRecA := httptest.NewRecorder()
	mux.ServeHTTP(sessRecA, sessReqA)
	if sessRecA.Code != http.StatusOK {
		t.Fatalf("session A failed: %d", sessRecA.Code)
	}
	_ = json.Unmarshal(sessRecA.Body.Bytes(), &identA)

	// 2. Sign up Shop Beta
	signupB := bytes.NewBufferString(`{
		"email": "beta_owner@example.com",
		"password": "PasswordBeta456!",
		"shop_name": "Beta Provisions"
	}`)
	reqB := httptest.NewRequest(http.MethodPost, "/api/auth/signup", signupB)
	reqB.Header.Set("Content-Type", "application/json")
	reqB.Header.Set("X-ListenLedger-Request", "1")
	recB := httptest.NewRecorder()
	mux.ServeHTTP(recB, reqB)
	if recB.Code != http.StatusOK {
		t.Fatalf("signup B failed: %d: %s", recB.Code, recB.Body.String())
	}
	cookieB := recB.Result().Cookies()[0]

	var identB storage.Identity
	sessReqB := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessReqB.AddCookie(cookieB)
	sessRecB := httptest.NewRecorder()
	mux.ServeHTTP(sessRecB, sessReqB)
	if sessRecB.Code != http.StatusOK {
		t.Fatalf("session B failed: %d", sessRecB.Code)
	}
	_ = json.Unmarshal(sessRecB.Body.Bytes(), &identB)

	// 3. Shop A creates Product A
	prodABody := bytes.NewBufferString(`{
		"name": "Milo 500g",
		"sku": "MILO-500",
		"price": {"amount": 250000, "currency": "NGN"},
		"quantity": 50,
		"base_unit": "pack"
	}`)
	prodReqA := httptest.NewRequest(http.MethodPost, "/api/products", prodABody)
	prodReqA.Header.Set("Content-Type", "application/json")
	prodReqA.Header.Set("X-ListenLedger-Request", "1")
	prodReqA.AddCookie(cookieA)
	prodRecA := httptest.NewRecorder()
	mux.ServeHTTP(prodRecA, prodReqA)
	if prodRecA.Code != http.StatusCreated {
		t.Fatalf("create product A failed: %d: %s", prodRecA.Code, prodRecA.Body.String())
	}
	var prodA models.Product
	_ = json.Unmarshal(prodRecA.Body.Bytes(), &prodA)

	// 4. Shop A pairs Device A
	devABody := bytes.NewBufferString(`{"name": "Alpha Counter Mic"}`)
	devReqA := httptest.NewRequest(http.MethodPost, "/api/devices", devABody)
	devReqA.Header.Set("Content-Type", "application/json")
	devReqA.Header.Set("X-ListenLedger-Request", "1")
	devReqA.AddCookie(cookieA)
	devRecA := httptest.NewRecorder()
	mux.ServeHTTP(devRecA, devReqA)
	if devRecA.Code != http.StatusCreated {
		t.Fatalf("pair device A failed: %d: %s", devRecA.Code, devRecA.Body.String())
	}
	var devA struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(devRecA.Body.Bytes(), &devA)

	// 5. Device A records Transaction A
	capBodyA := bytes.NewBufferString(`{
		"capture_id": "cap_iso_alpha_1",
		"transcript": "One Milo, two thousand five hundred naira. Thank you."
	}`)
	capReqA := httptest.NewRequest(http.MethodPost, "/api/captures", capBodyA)
	capReqA.Header.Set("Content-Type", "application/json")
	capReqA.Header.Set("Authorization", "Bearer "+devA.Token)
	capRecA := httptest.NewRecorder()
	mux.ServeHTTP(capRecA, capReqA)
	if capRecA.Code != http.StatusCreated {
		t.Fatalf("capture A failed: %d: %s", capRecA.Code, capRecA.Body.String())
	}
	var capRespA struct {
		Transactions []models.Transaction `json:"transactions"`
	}
	_ = json.Unmarshal(capRecA.Body.Bytes(), &capRespA)
	txnAID := ""
	if len(capRespA.Transactions) > 0 {
		txnAID = capRespA.Transactions[0].ID
	}

	return &tenantFixture{
		Store:    store,
		Handler:  mux,
		AuthSvc:  authSvc,
		ShopAID:  identA.ShopID,
		CookieA:  cookieA,
		ShopBID:  identB.ShopID,
		CookieB:  cookieB,
		DeviceA:  devA.ID,
		TokenA:   devA.Token,
		ProductA: prodA.ID,
		TxnA:     txnAID,
	}
}

// TestDeploymentIsolation_CrossTenantHermeticity proves that Shop Beta cannot inspect,
// mutate, or manipulate Shop Alpha's products, transactions, receipts, or devices.
func TestDeploymentIsolation_CrossTenantHermeticity(t *testing.T) {
	f := setupTenantFixture(t)

	// 1. Shop Beta queries catalogue -> Must NOT contain Product A
	invReq := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	invReq.AddCookie(f.CookieB)
	invRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(invRec, invReq)
	if invRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", invRec.Code)
	}
	var invResp struct {
		Products []models.Product `json:"products"`
		Count    int              `json:"count"`
	}
	_ = json.Unmarshal(invRec.Body.Bytes(), &invResp)
	if invResp.Count != 0 {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta saw %d products belonging to Shop Alpha", invResp.Count)
	}

	// 2. Shop Beta attempts to fetch Product A by ID -> 404 Not Found
	getProdReq := httptest.NewRequest(http.MethodGet, "/api/products/"+f.ProductA, nil)
	getProdReq.AddCookie(f.CookieB)
	getProdRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(getProdRec, getProdReq)
	if getProdRec.Code != http.StatusNotFound {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta accessed Product A, got %d", getProdRec.Code)
	}

	// 3. Shop Beta attempts to adjust Product A stock -> 404 Not Found
	adjBody := bytes.NewBufferString(`{"quantity": 100, "reason": "Malicious adjustment"}`)
	adjReq := httptest.NewRequest(http.MethodPost, "/api/products/"+f.ProductA+"/adjust", adjBody)
	adjReq.Header.Set("Content-Type", "application/json")
	adjReq.Header.Set("X-ListenLedger-Request", "1")
	adjReq.AddCookie(f.CookieB)
	adjRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(adjRec, adjReq)
	if adjRec.Code != http.StatusNotFound {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta adjusted Product A stock, got %d", adjRec.Code)
	}

	// 4. Shop Beta attempts to archive Product A -> 404 Not Found
	archReq := httptest.NewRequest(http.MethodPost, "/api/products/"+f.ProductA+"/archive", nil)
	archReq.Header.Set("X-ListenLedger-Request", "1")
	archReq.AddCookie(f.CookieB)
	archRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(archRec, archReq)
	if archRec.Code != http.StatusNotFound {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta archived Product A, got %d", archRec.Code)
	}

	// 5. Shop Beta queries transactions -> Must NOT contain Transaction A
	txnsReq := httptest.NewRequest(http.MethodGet, "/api/transactions", nil)
	txnsReq.AddCookie(f.CookieB)
	txnsRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(txnsRec, txnsReq)
	if txnsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", txnsRec.Code)
	}
	var txnsResp struct {
		Transactions []models.Transaction `json:"transactions"`
		Count        int                  `json:"count"`
	}
	_ = json.Unmarshal(txnsRec.Body.Bytes(), &txnsResp)
	if txnsResp.Count != 0 {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta saw %d transactions belonging to Shop Alpha", txnsResp.Count)
	}

	// 6. Shop Beta attempts to reverse Transaction A -> 404 Not Found
	revBody := bytes.NewBufferString(`{"reason": "Malicious reverse"}`)
	revReq := httptest.NewRequest(http.MethodPost, "/api/transactions/"+f.TxnA+"/reverse", revBody)
	revReq.Header.Set("Content-Type", "application/json")
	revReq.Header.Set("X-ListenLedger-Request", "1")
	revReq.AddCookie(f.CookieB)
	revRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(revRec, revReq)
	if revRec.Code != http.StatusNotFound {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta reversed Transaction A, got %d", revRec.Code)
	}

	// 7. Shop Beta attempts to inspect or revoke Device A -> 404 Not Found
	devListReq := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	devListReq.AddCookie(f.CookieB)
	devListRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(devListRec, devListReq)
	if devListRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", devListRec.Code)
	}
	var devListResp struct {
		Devices []any `json:"devices"`
	}
	_ = json.Unmarshal(devListRec.Body.Bytes(), &devListResp)
	if len(devListResp.Devices) != 0 {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta saw %d devices belonging to Shop Alpha", len(devListResp.Devices))
	}

	delDevReq := httptest.NewRequest(http.MethodPost, "/api/devices/"+f.DeviceA+"/revoke", nil)
	delDevReq.Header.Set("X-ListenLedger-Request", "1")
	delDevReq.AddCookie(f.CookieB)
	delDevRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(delDevRec, delDevReq)
	if delDevRec.Code != http.StatusNotFound {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta revoked Device A, got %d", delDevRec.Code)
	}

	// 8. Explicit shop_id query parameter spoofing: Session B with ?shop_id=ShopAID -> 401 Unauthorized
	spoofReq := httptest.NewRequest(http.MethodGet, "/api/inventory?shop_id="+f.ShopAID, nil)
	spoofReq.AddCookie(f.CookieB)
	spoofRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(spoofRec, spoofReq)
	if spoofRec.Code != http.StatusUnauthorized {
		t.Fatalf("CROSS-TENANT SPOOFING EXPLOIT: Shop Beta accessed Shop Alpha via query spoofing, got %d", spoofRec.Code)
	}
}

// TestDeviceRevocation_ImmediateIntakeRejection proves that revoking an edge device token
// immediately denies subsequent capture requests with HTTP 401 Unauthorized.
func TestDeviceRevocation_ImmediateIntakeRejection(t *testing.T) {
	f := setupTenantFixture(t)

	// 1. Submit capture while device is active -> 201 Created
	capBody1 := bytes.NewBufferString(`{
		"capture_id": "cap_active_dev_1",
		"transcript": "One Milo, two thousand five hundred naira. Thank you."
	}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/captures", capBody1)
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", "Bearer "+f.TokenA)
	rec1 := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// 2. Owner revokes device
	delReq := httptest.NewRequest(http.MethodPost, "/api/devices/"+f.DeviceA+"/revoke", nil)
	delReq.Header.Set("X-ListenLedger-Request", "1")
	delReq.AddCookie(f.CookieA)
	delRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on device revocation, got %d: %s", delRec.Code, delRec.Body.String())
	}

	// 3. Subsequent capture with revoked token MUST be rejected with HTTP 401 Unauthorized
	capBody2 := bytes.NewBufferString(`{
		"capture_id": "cap_revoked_dev_2",
		"transcript": "One Milo, two thousand five hundred naira. Thank you."
	}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/captures", capBody2)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+f.TokenA)
	rec2 := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("REVOCATION BYPASS: Revoked device token submitted capture successfully, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// TestAccountRecovery_SessionRevocation proves that recovering an account invalidates
// all existing sessions and rotates the one-time recovery code.
func TestAccountRecovery_SessionRevocation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "recovery_session_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	authSvc := &auth.Service{Store: store, SecureCookies: false}
	mux := http.NewServeMux()
	mux.Handle("/api/auth/", authSvc.Handler())

	// 1. Sign up account
	signupBody := bytes.NewBufferString(`{
		"email": "recover_owner@example.com",
		"password": "OldPassword123!",
		"shop_name": "Recovery Shop"
	}`)
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/signup", signupBody)
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-ListenLedger-Request", "1")
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("signup failed: %d: %s", rec1.Code, rec1.Body.String())
	}

	var signupResp struct {
		RecoveryCode string `json:"recovery_code"`
	}
	_ = json.Unmarshal(rec1.Body.Bytes(), &signupResp)
	if signupResp.RecoveryCode == "" {
		t.Fatalf("expected recovery code on signup")
	}
	initialRecoveryCode := signupResp.RecoveryCode

	// 2. Log in to obtain Session 1
	loginBody := bytes.NewBufferString(`{
		"email": "recover_owner@example.com",
		"password": "OldPassword123!"
	}`)
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody)
	reqLogin.Header.Set("Content-Type", "application/json")
	reqLogin.Header.Set("X-ListenLedger-Request", "1")
	recLogin := httptest.NewRecorder()
	mux.ServeHTTP(recLogin, reqLogin)
	if recLogin.Code != http.StatusOK {
		t.Fatalf("login failed: %d: %s", recLogin.Code, recLogin.Body.String())
	}
	sessionCookie1 := recLogin.Result().Cookies()[0]

	// Session 1 is valid
	sessReq1 := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessReq1.AddCookie(sessionCookie1)
	sessRec1 := httptest.NewRecorder()
	mux.ServeHTTP(sessRec1, sessReq1)
	if sessRec1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for session 1, got %d", sessRec1.Code)
	}

	// 3. Perform account recovery with valid recovery code and new password
	recoverBody := bytes.NewBufferString(fmt.Sprintf(`{
		"email": "recover_owner@example.com",
		"recovery_code": "%s",
		"password": "NewSecurePassword456!"
	}`, initialRecoveryCode))
	reqRec := httptest.NewRequest(http.MethodPost, "/api/auth/recover", recoverBody)
	reqRec.Header.Set("Content-Type", "application/json")
	reqRec.Header.Set("X-ListenLedger-Request", "1")
	recRec := httptest.NewRecorder()
	mux.ServeHTTP(recRec, reqRec)

	if recRec.Code != http.StatusOK {
		t.Fatalf("recovery failed: %d: %s", recRec.Code, recRec.Body.String())
	}

	var recResp struct {
		RecoveryCode string `json:"recovery_code"`
	}
	_ = json.Unmarshal(recRec.Body.Bytes(), &recResp)
	if recResp.RecoveryCode == "" || recResp.RecoveryCode == initialRecoveryCode {
		t.Fatalf("expected rotated recovery code; got %s", recResp.RecoveryCode)
	}

	// 4. Session 1 MUST NOW BE INVALID (revoked!)
	sessReqRevoked := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessReqRevoked.AddCookie(sessionCookie1)
	sessRecRevoked := httptest.NewRecorder()
	mux.ServeHTTP(sessRecRevoked, sessReqRevoked)

	if sessRecRevoked.Code != http.StatusUnauthorized {
		t.Fatalf("SESSION REVOCATION FAILURE: Previous session cookie remained active after recovery, got %d", sessRecRevoked.Code)
	}

	// 5. Old password MUST FAIL
	oldLoginBody := bytes.NewBufferString(`{
		"email": "recover_owner@example.com",
		"password": "OldPassword123!"
	}`)
	oldLoginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", oldLoginBody)
	oldLoginReq.Header.Set("Content-Type", "application/json")
	oldLoginReq.Header.Set("X-ListenLedger-Request", "1")
	oldLoginRec := httptest.NewRecorder()
	mux.ServeHTTP(oldLoginRec, oldLoginReq)
	if oldLoginRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected old password to fail, got %d", oldLoginRec.Code)
	}

	// 6. New password MUST SUCCEED
	newLoginBody := bytes.NewBufferString(`{
		"email": "recover_owner@example.com",
		"password": "NewSecurePassword456!"
	}`)
	newLoginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", newLoginBody)
	newLoginReq.Header.Set("Content-Type", "application/json")
	newLoginReq.Header.Set("X-ListenLedger-Request", "1")
	newLoginRec := httptest.NewRecorder()
	mux.ServeHTTP(newLoginRec, newLoginReq)
	if newLoginRec.Code != http.StatusOK {
		t.Fatalf("new password login failed: %d: %s", newLoginRec.Code, newLoginRec.Body.String())
	}
}
