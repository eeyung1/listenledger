package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/storage"
)

type recFixture struct {
	Store   *storage.Store
	Handler http.Handler
	ShopID  string
	Cookie  *http.Cookie
	Token   string
	DeviceA string
}

func setupRecFixture(t *testing.T) *recFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "rec_test.db")
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
	signup := bytes.NewBufferString(`{
		"email": "pilot@mamadolu.ng",
		"password": "PilotPassword123!",
		"shop_name": "Mama Tolu Provisions"
	}`)
	signupReq := httptest.NewRequest(http.MethodPost, "/api/auth/signup", signup)
	signupReq.Header.Set("Content-Type", "application/json")
	signupReq.Header.Set("X-ListenLedger-Request", "1")
	signupRec := httptest.NewRecorder()
	mux.ServeHTTP(signupRec, signupReq)
	if signupRec.Code != http.StatusOK {
		t.Fatalf("signup failed: %d: %s", signupRec.Code, signupRec.Body.String())
	}
	cookie := signupRec.Result().Cookies()[0]

	var ident storage.Identity
	sessReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessReq.AddCookie(cookie)
	sessRec := httptest.NewRecorder()
	mux.ServeHTTP(sessRec, sessReq)
	if sessRec.Code != http.StatusOK {
		t.Fatalf("session failed: %d", sessRec.Code)
	}
	_ = json.Unmarshal(sessRec.Body.Bytes(), &ident)

	// 2. Insert Products
	prod1Body := bytes.NewBufferString(`{
		"name": "Milo 500g",
		"sku": "MILO-500",
		"aliases": ["milo", "nestle milo"],
		"price": {"amount": 250000, "currency": "NGN"},
		"quantity": 20,
		"base_unit": "pack",
		"low_stock_threshold": 5
	}`)
	prod1Req := httptest.NewRequest(http.MethodPost, "/api/products", prod1Body)
	prod1Req.Header.Set("Content-Type", "application/json")
	prod1Req.Header.Set("X-ListenLedger-Request", "1")
	prod1Req.AddCookie(cookie)
	prod1Rec := httptest.NewRecorder()
	mux.ServeHTTP(prod1Rec, prod1Req)
	if prod1Rec.Code != http.StatusCreated {
		t.Fatalf("create product 1 failed: %d: %s", prod1Rec.Code, prod1Rec.Body.String())
	}

	prod2Body := bytes.NewBufferString(`{
		"name": "Peak Evaporated Milk",
		"sku": "PEAK-EVAP",
		"aliases": ["peak milk", "evap milk"],
		"price": {"amount": 80000, "currency": "NGN"},
		"quantity": 30,
		"base_unit": "pack",
		"low_stock_threshold": 5
	}`)
	prod2Req := httptest.NewRequest(http.MethodPost, "/api/products", prod2Body)
	prod2Req.Header.Set("Content-Type", "application/json")
	prod2Req.Header.Set("X-ListenLedger-Request", "1")
	prod2Req.AddCookie(cookie)
	prod2Rec := httptest.NewRecorder()
	mux.ServeHTTP(prod2Rec, prod2Req)
	if prod2Rec.Code != http.StatusCreated {
		t.Fatalf("create product 2 failed: %d: %s", prod2Rec.Code, prod2Rec.Body.String())
	}

	// 3. Pair device
	devBody := bytes.NewBufferString(`{"name": "Counter 1 Pilot Mic"}`)
	devReq := httptest.NewRequest(http.MethodPost, "/api/devices", devBody)
	devReq.Header.Set("Content-Type", "application/json")
	devReq.Header.Set("X-ListenLedger-Request", "1")
	devReq.AddCookie(cookie)
	devRec := httptest.NewRecorder()
	mux.ServeHTTP(devRec, devReq)
	if devRec.Code != http.StatusCreated {
		t.Fatalf("pair device failed: %d: %s", devRec.Code, devRec.Body.String())
	}
	var dev struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(devRec.Body.Bytes(), &dev)

	return &recFixture{
		Store:   store,
		Handler: mux,
		ShopID:  ident.ShopID,
		Cookie:  cookie,
		Token:   dev.Token,
		DeviceA: dev.ID,
	}
}

func TestPilotReconciliation_PerfectMatchAndGatePass(t *testing.T) {
	f := setupRecFixture(t)

	now := time.Now().UTC()
	t1 := now.Add(-30 * time.Minute)
	t2 := now.Add(-20 * time.Minute)

	// 1. Record 2 confirmed transactions via edge intake
	cap1 := fmt.Sprintf(`{
		"capture_id": "cap_rec_1",
		"device_id": "%s",
		"occurred_at": "%s",
		"transcript": "One Milo, two thousand five hundred naira. Thank you."
	}`, f.DeviceA, t1.Format(time.RFC3339))
	req1 := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(cap1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", "Bearer "+f.Token)
	rec1 := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on capture 1, got %d: %s", rec1.Code, rec1.Body.String())
	}

	cap2 := fmt.Sprintf(`{
		"capture_id": "cap_rec_2",
		"device_id": "%s",
		"occurred_at": "%s",
		"transcript": "Two Peak milk, one thousand six hundred naira. Thank you."
	}`, f.DeviceA, t2.Format(time.RFC3339))
	req2 := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(cap2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+f.Token)
	rec2 := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on capture 2, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// 2. Submit independent paper ledger entries that match perfectly:
	// Milo: 1 @ 250,000 kobo (stock was 20 -> now 19)
	// Peak milk: 2 @ 160,000 kobo (stock was 30 -> now 28)
	reconcilePayload := fmt.Sprintf(`{
		"period_start": "%s",
		"period_end": "%s",
		"independent_source": "Paper Counter Daily Log",
		"conducted_by": "Mama Tolu & Field Auditor",
		"independent_records": [
			{
				"occurred_at": "%s",
				"sku_or_name": "MILO-500",
				"type": "sale",
				"quantity": 1,
				"unit": "pack",
				"amount": 250000
			},
			{
				"occurred_at": "%s",
				"sku_or_name": "PEAK-EVAP",
				"type": "sale",
				"quantity": 2,
				"unit": "pack",
				"amount": 160000
			}
		],
		"physical_stock_counts": [
			{ "sku_or_name": "MILO-500", "physical_count": 19 },
			{ "sku_or_name": "PEAK-EVAP", "physical_count": 28 }
		],
		"notes": "End of trading day dual-entry reconciliation."
	}`, now.Add(-1*time.Hour).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339), t1.Format(time.RFC3339), t2.Format(time.RFC3339))

	recReq := httptest.NewRequest(http.MethodPost, "/api/pilot/reconcile", bytes.NewBufferString(reconcilePayload))
	recReq.Header.Set("Content-Type", "application/json")
	recReq.Header.Set("X-ListenLedger-Request", "1")
	recReq.AddCookie(f.Cookie)
	recRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(recRec, recReq)

	if recRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on reconciliation, got %d: %s", recRec.Code, recRec.Body.String())
	}

	var resp struct {
		Reconciliation storage.PilotReconciliation `json:"reconciliation"`
		GateResults    []api.GateCheckResult       `json:"gate_results"`
		AllGatesPassed bool                        `json:"all_gates_passed"`
	}
	if err := json.Unmarshal(recRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Reconciliation.Status != "reconciled" {
		t.Fatalf("expected status 'reconciled', got %q", resp.Reconciliation.Status)
	}
	if resp.Reconciliation.MatchedRecords != 2 {
		t.Fatalf("expected 2 matched records, got %d", resp.Reconciliation.MatchedRecords)
	}
	if resp.Reconciliation.MissedCaptures != 0 {
		t.Fatalf("expected 0 missed captures, got %d", resp.Reconciliation.MissedCaptures)
	}
	if resp.Reconciliation.FalseCaptures != 0 {
		t.Fatalf("expected 0 false captures, got %d", resp.Reconciliation.FalseCaptures)
	}
	if resp.Reconciliation.RevenueDiscrepancy != 0 {
		t.Fatalf("expected 0 revenue discrepancy, got %d", resp.Reconciliation.RevenueDiscrepancy)
	}
	if resp.Reconciliation.StockDiscrepancyCount != 0 {
		t.Fatalf("expected 0 stock discrepancy, got %d", resp.Reconciliation.StockDiscrepancyCount)
	}
	if !resp.AllGatesPassed {
		t.Fatalf("expected all gates to pass, got %+v", resp.GateResults)
	}
}

func TestPilotReconciliation_DiscrepancyDetectionAndInvestigationWorkflow(t *testing.T) {
	f := setupRecFixture(t)

	now := time.Now().UTC()
	t1 := now.Add(-40 * time.Minute)

	// 1. Edge recorded 1 sale: One Milo @ 2,500 NGN
	cap1 := fmt.Sprintf(`{
		"capture_id": "cap_disc_1",
		"device_id": "%s",
		"occurred_at": "%s",
		"transcript": "One Milo, two thousand five hundred naira. Thank you."
	}`, f.DeviceA, t1.Format(time.RFC3339))
	req1 := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(cap1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", "Bearer "+f.Token)
	rec1 := httptest.NewRecorder()
	f.Handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on capture 1, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// 2. Independent paper ledger has:
	// - 1 Milo (matches cap1)
	// - 1 Peak milk @ 800 NGN (MISSING from ListenLedger - cashier whispered or mic unhooked)
	// Plus physical stock count shows Milo is 18 (expected 19 -> 1 tin variance)
	reconcilePayload := fmt.Sprintf(`{
		"period_start": "%s",
		"period_end": "%s",
		"independent_source": "Paper Register",
		"conducted_by": "Auditor",
		"independent_records": [
			{
				"occurred_at": "%s",
				"sku_or_name": "MILO-500",
				"type": "sale",
				"quantity": 1,
				"unit": "pack",
				"amount": 250000
			},
			{
				"occurred_at": "%s",
				"sku_or_name": "PEAK-EVAP",
				"type": "sale",
				"quantity": 1,
				"unit": "pack",
				"amount": 80000
			}
		],
		"physical_stock_counts": [
			{ "sku_or_name": "MILO-500", "physical_count": 18 },
			{ "sku_or_name": "PEAK-EVAP", "physical_count": 30 }
		],
		"notes": "Session with discrepancies"
	}`, now.Add(-1*time.Hour).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339), t1.Format(time.RFC3339), t1.Add(5*time.Minute).Format(time.RFC3339))

	recReq := httptest.NewRequest(http.MethodPost, "/api/pilot/reconcile", bytes.NewBufferString(reconcilePayload))
	recReq.Header.Set("Content-Type", "application/json")
	recReq.Header.Set("X-ListenLedger-Request", "1")
	recReq.AddCookie(f.Cookie)
	recRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(recRec, recReq)

	if recRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on reconciliation, got %d: %s", recRec.Code, recRec.Body.String())
	}

	var resp struct {
		Reconciliation storage.PilotReconciliation `json:"reconciliation"`
		GateResults    []api.GateCheckResult       `json:"gate_results"`
		AllGatesPassed bool                        `json:"all_gates_passed"`
	}
	if err := json.Unmarshal(recRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	// Status must be 'discrepant' because unexplained discrepancies exist
	if resp.Reconciliation.Status != "discrepant" {
		t.Fatalf("expected status 'discrepant', got %q", resp.Reconciliation.Status)
	}
	if resp.Reconciliation.MissedCaptures != 1 {
		t.Fatalf("expected 1 missed capture, got %d", resp.Reconciliation.MissedCaptures)
	}
	if resp.Reconciliation.StockDiscrepancyCount != 1 {
		t.Fatalf("expected 1 stock discrepancy, got %d", resp.Reconciliation.StockDiscrepancyCount)
	}
	if len(resp.Reconciliation.Discrepancies) != 2 {
		t.Fatalf("expected 2 discrepancies in response, got %d", len(resp.Reconciliation.Discrepancies))
	}

	// 3. Investigate the discrepancies line-by-line
	for _, d := range resp.Reconciliation.Discrepancies {
		rootCause := "acoustic_noise"
		notes := "Investigated root cause"
		if d.DiscrepancyType == "stock_variance" {
			rootCause = "physical_shrinkage"
			notes = "Damaged tin removed from shelf prior to store open"
		} else if d.DiscrepancyType == "missed_capture" {
			rootCause = "cashier_protocol_drift"
			notes = "Cashier whispered Peak milk sale while customer was speaking"
		}

		invPayload := fmt.Sprintf(`{
			"root_cause": "%s",
			"investigation_notes": "%s",
			"investigated_by": "Mama Tolu"
		}`, rootCause, notes)

		invReq := httptest.NewRequest(http.MethodPost, "/api/pilot/discrepancies/"+d.ID+"/investigate", bytes.NewBufferString(invPayload))
		invReq.Header.Set("Content-Type", "application/json")
		invReq.Header.Set("X-ListenLedger-Request", "1")
		invReq.AddCookie(f.Cookie)
		invRec := httptest.NewRecorder()
		f.Handler.ServeHTTP(invRec, invReq)

		if invRec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on discrepancy investigation, got %d: %s", invRec.Code, invRec.Body.String())
		}
	}

	// 4. Verify reconciliation session is now marked 'reconciled' because all differences are explained!
	getReq := httptest.NewRequest(http.MethodGet, "/api/pilot/reconciliations/"+resp.Reconciliation.ID, nil)
	getReq.AddCookie(f.Cookie)
	getRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on get reconciliation, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var updatedRec storage.PilotReconciliation
	if err := json.Unmarshal(getRec.Body.Bytes(), &updatedRec); err != nil {
		t.Fatalf("unmarshal updated rec: %v", err)
	}

	if updatedRec.Status != "reconciled" {
		t.Fatalf("expected status to transition to 'reconciled' after all differences investigated, got %q", updatedRec.Status)
	}
}

func TestPilotCohort_ReadinessEvaluationAndPhaseProgression(t *testing.T) {
	f := setupRecFixture(t)

	// 1. Initial readiness evaluation: should be BLOCKED because staff is untrained and no reconciliation done
	evalReq1 := httptest.NewRequest(http.MethodPost, "/api/pilot/cohort/evaluate-readiness", bytes.NewBufferString(`{"promote": false}`))
	evalReq1.Header.Set("Content-Type", "application/json")
	evalReq1.Header.Set("X-ListenLedger-Request", "1")
	evalReq1.AddCookie(f.Cookie)
	evalRec1 := httptest.NewRecorder()
	f.Handler.ServeHTTP(evalRec1, evalReq1)

	if evalRec1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", evalRec1.Code, evalRec1.Body.String())
	}

	var evalResp1 api.CohortReadinessResponse
	_ = json.Unmarshal(evalRec1.Body.Bytes(), &evalResp1)
	if evalResp1.ReadinessStatus != "blocked" {
		t.Fatalf("expected 'blocked', got %q", evalResp1.ReadinessStatus)
	}

	// 2. Complete staff cashier drill
	drillBody := bytes.NewBufferString(`{
		"scenario_type": "clean_sale",
		"transcript": "Two packs Milo, five thousand naira. Thank you."
	}`)
	drillReq := httptest.NewRequest(http.MethodPost, "/api/onboarding/drill", drillBody)
	drillReq.Header.Set("Content-Type", "application/json")
	drillReq.Header.Set("X-ListenLedger-Request", "1")
	drillReq.AddCookie(f.Cookie)
	drillRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(drillRec, drillReq)
	if drillRec.Code != http.StatusOK {
		t.Fatalf("drill failed: %d: %s", drillRec.Code, drillRec.Body.String())
	}

	// 3. Agree target gates
	gatesBody := bytes.NewBufferString(`{
		"min_full_transaction_accuracy": 90.0,
		"min_sku_accuracy": 95.0,
		"min_quantity_accuracy": 95.0,
		"min_unit_accuracy": 95.0,
		"min_amount_accuracy": 95.0,
		"max_false_capture_rate": 3.0,
		"max_missed_transaction_rate": 5.0,
		"max_correction_rate": 10.0,
		"min_protocol_compliance_rate": 85.0,
		"max_p95_latency_ms": 1500,
		"agreed_by": "Mama Tolu"
	}`)
	gatesReq := httptest.NewRequest(http.MethodPut, "/api/settings/target-gates", gatesBody)
	gatesReq.Header.Set("Content-Type", "application/json")
	gatesReq.Header.Set("X-ListenLedger-Request", "1")
	gatesReq.AddCookie(f.Cookie)
	gatesRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(gatesRec, gatesReq)
	if gatesRec.Code != http.StatusOK {
		t.Fatalf("gates agreement failed: %d", gatesRec.Code)
	}

	// 4. Run clean reconciliation session with 0 discrepancies
	now := time.Now().UTC()
	cleanRecPayload := fmt.Sprintf(`{
		"period_start": "%s",
		"period_end": "%s",
		"independent_source": "Paper Counter Ledger",
		"independent_records": [],
		"physical_stock_counts": [
			{ "sku_or_name": "MILO-500", "physical_count": 20 },
			{ "sku_or_name": "PEAK-EVAP", "physical_count": 30 }
		]
	}`, now.Add(-10*time.Minute).Format(time.RFC3339), now.Format(time.RFC3339))
	cleanReq := httptest.NewRequest(http.MethodPost, "/api/pilot/reconcile", bytes.NewBufferString(cleanRecPayload))
	cleanReq.Header.Set("Content-Type", "application/json")
	cleanReq.Header.Set("X-ListenLedger-Request", "1")
	cleanReq.AddCookie(f.Cookie)
	cleanRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(cleanRec, cleanReq)
	if cleanRec.Code != http.StatusCreated {
		t.Fatalf("reconcile failed: %d", cleanRec.Code)
	}

	// 5. Evaluate readiness again with promote: true -> should be QUALIFIED and promoted to Phase 2!
	evalReq2 := httptest.NewRequest(http.MethodPost, "/api/pilot/cohort/evaluate-readiness", bytes.NewBufferString(`{"promote": true}`))
	evalReq2.Header.Set("Content-Type", "application/json")
	evalReq2.Header.Set("X-ListenLedger-Request", "1")
	evalReq2.AddCookie(f.Cookie)
	evalRec2 := httptest.NewRecorder()
	f.Handler.ServeHTTP(evalRec2, evalReq2)

	if evalRec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", evalRec2.Code, evalRec2.Body.String())
	}

	var evalResp2 api.CohortReadinessResponse
	_ = json.Unmarshal(evalRec2.Body.Bytes(), &evalResp2)
	if evalResp2.ReadinessStatus != "qualified" {
		t.Fatalf("expected 'qualified', got %q", evalResp2.ReadinessStatus)
	}
	if evalResp2.CurrentPhase != "phase2_dual_shop" {
		t.Fatalf("expected promotion to 'phase2_dual_shop', got %q", evalResp2.CurrentPhase)
	}
}

func TestPilotReconciliation_CrossTenantIsolation(t *testing.T) {
	f := setupRecFixture(t)

	// Create second tenant Shop Beta via signup
	signupBeta := bytes.NewBufferString(`{
		"email": "beta_owner@example.com",
		"password": "PasswordBeta456!",
		"shop_name": "Beta Provisions"
	}`)
	reqBeta := httptest.NewRequest(http.MethodPost, "/api/auth/signup", signupBeta)
	reqBeta.Header.Set("Content-Type", "application/json")
	reqBeta.Header.Set("X-ListenLedger-Request", "1")
	recBeta := httptest.NewRecorder()
	f.Handler.ServeHTTP(recBeta, reqBeta)
	if recBeta.Code != http.StatusOK {
		t.Fatalf("signup Beta failed: %d", recBeta.Code)
	}
	cookieBeta := recBeta.Result().Cookies()[0]

	// Create reconciliation under Shop Alpha
	now := time.Now().UTC()
	reconcilePayload := fmt.Sprintf(`{
		"period_start": "%s",
		"period_end": "%s",
		"independent_source": "Paper Register",
		"independent_records": [],
		"physical_stock_counts": []
	}`, now.Add(-10*time.Minute).Format(time.RFC3339), now.Format(time.RFC3339))

	recReq := httptest.NewRequest(http.MethodPost, "/api/pilot/reconcile", bytes.NewBufferString(reconcilePayload))
	recReq.Header.Set("Content-Type", "application/json")
	recReq.Header.Set("X-ListenLedger-Request", "1")
	recReq.AddCookie(f.Cookie)
	recRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(recRec, recReq)

	var resp struct {
		Reconciliation storage.PilotReconciliation `json:"reconciliation"`
	}
	_ = json.Unmarshal(recRec.Body.Bytes(), &resp)
	alphaRecID := resp.Reconciliation.ID

	// Shop Beta attempts to access Shop Alpha's reconciliation -> 404 Not Found
	getReq := httptest.NewRequest(http.MethodGet, "/api/pilot/reconciliations/"+alphaRecID, nil)
	getReq.AddCookie(cookieBeta)
	getRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusNotFound {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta accessed Shop Alpha's reconciliation, got %d", getRec.Code)
	}

	// Shop Beta lists reconciliations -> 0 items
	listReq := httptest.NewRequest(http.MethodGet, "/api/pilot/reconciliations", nil)
	listReq.AddCookie(cookieBeta)
	listRec := httptest.NewRecorder()
	f.Handler.ServeHTTP(listRec, listReq)

	var listResp struct {
		Reconciliations []storage.PilotReconciliation `json:"reconciliations"`
	}
	_ = json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Reconciliations) != 0 {
		t.Fatalf("CROSS-TENANT LEAKAGE: Shop Beta saw Shop Alpha's reconciliations: %d", len(listResp.Reconciliations))
	}
}
