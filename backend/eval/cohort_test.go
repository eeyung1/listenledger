package eval_test

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

type cohortPilotApp struct {
	Store   *storage.Store
	Handler http.Handler
}

func setupCohortPilotApp(t *testing.T) *cohortPilotApp {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cohort_eval.db")
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

	return &cohortPilotApp{
		Store:   store,
		Handler: mux,
	}
}

type shopPilotContext struct {
	ShopID   string
	Cookie   *http.Cookie
	DevToken string
	DevID    string
}

func onboardPilotShop(t *testing.T, app *cohortPilotApp, email, name string) *shopPilotContext {
	t.Helper()
	// 1. Signup
	signupBody := fmt.Sprintf(`{"email":"%s","password":"PasswordPilot123!","shop_name":"%s"}`, email, name)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(signupBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ListenLedger-Request", "1")
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signup %s failed: %d: %s", email, rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	// 2. Fetch session identity
	sessReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessReq.AddCookie(cookie)
	sessRec := httptest.NewRecorder()
	app.Handler.ServeHTTP(sessRec, sessReq)
	var ident storage.Identity
	_ = json.Unmarshal(sessRec.Body.Bytes(), &ident)

	// 3. Populate catalogue products
	miloBody := `{
		"name": "Milo 500g",
		"sku": "MILO-500",
		"aliases": ["milo", "nestle milo"],
		"price": {"amount": 250000, "currency": "NGN"},
		"quantity": 50,
		"base_unit": "pack",
		"low_stock_threshold": 10
	}`
	pReq := httptest.NewRequest(http.MethodPost, "/api/products", bytes.NewBufferString(miloBody))
	pReq.Header.Set("Content-Type", "application/json")
	pReq.Header.Set("X-ListenLedger-Request", "1")
	pReq.AddCookie(cookie)
	pRec := httptest.NewRecorder()
	app.Handler.ServeHTTP(pRec, pReq)
	if pRec.Code != http.StatusCreated {
		t.Fatalf("create product failed: %d: %s", pRec.Code, pRec.Body.String())
	}

	// 4. Pair counter microphone
	devBody := `{"name": "Counter 1 USB Mic"}`
	dReq := httptest.NewRequest(http.MethodPost, "/api/devices", bytes.NewBufferString(devBody))
	dReq.Header.Set("Content-Type", "application/json")
	dReq.Header.Set("X-ListenLedger-Request", "1")
	dReq.AddCookie(cookie)
	dRec := httptest.NewRecorder()
	app.Handler.ServeHTTP(dRec, dReq)
	if dRec.Code != http.StatusCreated {
		t.Fatalf("pair device failed: %d: %s", dRec.Code, dRec.Body.String())
	}
	var dev struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(dRec.Body.Bytes(), &dev)

	// 5. Complete cashier protocol training drill
	drillBody := `{"scenario_type":"clean_sale","transcript":"Two packs Milo, five thousand naira. Thank you."}`
	drReq := httptest.NewRequest(http.MethodPost, "/api/onboarding/drill", bytes.NewBufferString(drillBody))
	drReq.Header.Set("Content-Type", "application/json")
	drReq.Header.Set("X-ListenLedger-Request", "1")
	drReq.AddCookie(cookie)
	drRec := httptest.NewRecorder()
	app.Handler.ServeHTTP(drRec, drReq)
	if drRec.Code != http.StatusOK {
		t.Fatalf("drill failed: %d: %s", drRec.Code, drRec.Body.String())
	}

	// 6. Formally agree target gates
	gatesBody := `{
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
		"agreed_by": "Owner Signature"
	}`
	gReq := httptest.NewRequest(http.MethodPut, "/api/settings/target-gates", bytes.NewBufferString(gatesBody))
	gReq.Header.Set("Content-Type", "application/json")
	gReq.Header.Set("X-ListenLedger-Request", "1")
	gReq.AddCookie(cookie)
	gRec := httptest.NewRecorder()
	app.Handler.ServeHTTP(gRec, gReq)
	if gRec.Code != http.StatusOK {
		t.Fatalf("agree gates failed: %d: %s", gRec.Code, gRec.Body.String())
	}

	return &shopPilotContext{
		ShopID:   ident.ShopID,
		Cookie:   cookie,
		DevToken: dev.Token,
		DevID:    dev.ID,
	}
}

// TestMultiShopCohortTrial_PhasedProgressionAndExpansionGates executes the complete
// pilot journey from README §11:
// 1. Controlled trial in 1 shop (Mama Tolu Provisions) alongside independent paper records.
// 2. Investigates unexplained discrepancies to 0 root causes.
// 3. Qualifies and unlocks Phase 2 (1–2 shops alongside independent records, onboarding Emeka Supermarket).
// 4. Verifies expansion gates toward 5 and 10–15 shops.
func TestMultiShopCohortTrial_PhasedProgressionAndExpansionGates(t *testing.T) {
	app := setupCohortPilotApp(t)

	// =========================================================================
	// PHASE 1: Controlled Shop 1 Trial (Mama Tolu Provisions)
	// =========================================================================
	shop1 := onboardPilotShop(t, app, "mama_tolu@lagos.ng", "Mama Tolu Provisions")

	now := time.Now().UTC()
	t1 := now.Add(-30 * time.Minute)

	// Ambient speech capture records 1 sale: One Milo @ 2500 NGN
	cap1 := fmt.Sprintf(`{
		"capture_id": "cap_shop1_01",
		"device_id": "%s",
		"occurred_at": "%s",
		"transcript": "One Milo, two thousand five hundred naira. Thank you."
	}`, shop1.DevID, t1.Format(time.RFC3339))
	req1 := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(cap1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", "Bearer "+shop1.DevToken)
	rec1 := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("shop 1 capture failed: %d: %s", rec1.Code, rec1.Body.String())
	}

	// Independent paper ledger had 2 sales (one was whispered and missed by ambient capture):
	rec1Body := fmt.Sprintf(`{
		"period_start": "%s",
		"period_end": "%s",
		"independent_source": "Paper Counter Register",
		"conducted_by": "Mama Tolu & Auditor",
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
				"sku_or_name": "MILO-500",
				"type": "sale",
				"quantity": 1,
				"unit": "pack",
				"amount": 250000
			}
		],
		"physical_stock_counts": [
			{ "sku_or_name": "MILO-500", "physical_count": 48 }
		]
	}`, now.Add(-1*time.Hour).Format(time.RFC3339), now.Format(time.RFC3339), t1.Format(time.RFC3339), t1.Add(5*time.Minute).Format(time.RFC3339))

	rReq1 := httptest.NewRequest(http.MethodPost, "/api/pilot/reconcile", bytes.NewBufferString(rec1Body))
	rReq1.Header.Set("Content-Type", "application/json")
	rReq1.Header.Set("X-ListenLedger-Request", "1")
	rReq1.AddCookie(shop1.Cookie)
	rRec1 := httptest.NewRecorder()
	app.Handler.ServeHTTP(rRec1, rReq1)

	if rRec1.Code != http.StatusCreated {
		t.Fatalf("reconcile shop 1 failed: %d: %s", rRec1.Code, rRec1.Body.String())
	}

	var recResp1 struct {
		Reconciliation storage.PilotReconciliation `json:"reconciliation"`
		AllGatesPassed bool                        `json:"all_gates_passed"`
	}
	_ = json.Unmarshal(rRec1.Body.Bytes(), &recResp1)

	// Invariants: 1 matched, 1 missed capture, 1 physical stock variance (actual system stock is 49, physical count 48)
	if recResp1.Reconciliation.Status != "discrepant" {
		t.Fatalf("expected discrepant status prior to investigation, got %q", recResp1.Reconciliation.Status)
	}

	// Attempting to qualify for Phase 2 MUST BE BLOCKED while unexplained differences exist
	evalReq := httptest.NewRequest(http.MethodPost, "/api/pilot/cohort/evaluate-readiness", bytes.NewBufferString(`{"promote":true}`))
	evalReq.Header.Set("Content-Type", "application/json")
	evalReq.Header.Set("X-ListenLedger-Request", "1")
	evalReq.AddCookie(shop1.Cookie)
	evalRec := httptest.NewRecorder()
	app.Handler.ServeHTTP(evalRec, evalReq)

	var evalResp api.CohortReadinessResponse
	_ = json.Unmarshal(evalRec.Body.Bytes(), &evalResp)
	if evalResp.ReadinessStatus != "blocked" {
		t.Fatalf("expected readiness status 'blocked' while differences remain unexplained, got %q", evalResp.ReadinessStatus)
	}

	// =========================================================================
	// INVESTIGATE EVERY UNEXPLAINED DIFFERENCE (README §11 requirement)
	// =========================================================================
	for _, d := range recResp1.Reconciliation.Discrepancies {
		rootCause := "cashier_protocol_drift"
		notes := "Investigated: cashier whispered sale away from boundary mic"
		if d.DiscrepancyType == "stock_variance" {
			rootCause = "physical_shrinkage"
			notes = "Investigated: 1 tin damaged by morning delivery and set aside"
		}

		invBody := fmt.Sprintf(`{"root_cause":"%s","investigation_notes":"%s","investigated_by":"Field Auditor"}`, rootCause, notes)
		invReq := httptest.NewRequest(http.MethodPost, "/api/pilot/discrepancies/"+d.ID+"/investigate", bytes.NewBufferString(invBody))
		invReq.Header.Set("Content-Type", "application/json")
		invReq.Header.Set("X-ListenLedger-Request", "1")
		invReq.AddCookie(shop1.Cookie)
		invRec := httptest.NewRecorder()
		app.Handler.ServeHTTP(invRec, invReq)
		if invRec.Code != http.StatusOK {
			t.Fatalf("investigate failed: %d: %s", invRec.Code, invRec.Body.String())
		}
	}

	// Verify Shop 1 reconciliation is now 'reconciled'
	chkReq := httptest.NewRequest(http.MethodGet, "/api/pilot/reconciliations/"+recResp1.Reconciliation.ID, nil)
	chkReq.AddCookie(shop1.Cookie)
	chkRec := httptest.NewRecorder()
	app.Handler.ServeHTTP(chkRec, chkReq)
	var recheckedRec storage.PilotReconciliation
	_ = json.Unmarshal(chkRec.Body.Bytes(), &recheckedRec)
	if recheckedRec.Status != "reconciled" {
		t.Fatalf("expected 'reconciled' after all discrepancies investigated, got %q", recheckedRec.Status)
	}

	// Promote Shop 1 to Phase 2
	evalReq2 := httptest.NewRequest(http.MethodPost, "/api/pilot/cohort/evaluate-readiness", bytes.NewBufferString(`{"promote":true}`))
	evalReq2.Header.Set("Content-Type", "application/json")
	evalReq2.Header.Set("X-ListenLedger-Request", "1")
	evalReq2.AddCookie(shop1.Cookie)
	evalRec2 := httptest.NewRecorder()
	app.Handler.ServeHTTP(evalRec2, evalReq2)

	var evalResp2 api.CohortReadinessResponse
	_ = json.Unmarshal(evalRec2.Body.Bytes(), &evalResp2)
	if evalResp2.ReadinessStatus != "qualified" || evalResp2.CurrentPhase != "phase2_dual_shop" {
		t.Fatalf("expected Shop 1 to qualify and promote to phase2_dual_shop, got status=%q phase=%q", evalResp2.ReadinessStatus, evalResp2.CurrentPhase)
	}

	// =========================================================================
	// PHASE 2: 1–2 Shops (Onboard Shop 2: Emeka Supermarket)
	// =========================================================================
	shop2 := onboardPilotShop(t, app, "emeka@onitsha.ng", "Emeka Supermarket")

	// Shop 2 performs clean dual-entry shift
	cap2 := fmt.Sprintf(`{
		"capture_id": "cap_shop2_01",
		"device_id": "%s",
		"occurred_at": "%s",
		"transcript": "One Milo, two thousand five hundred naira. Thank you."
	}`, shop2.DevID, t1.Format(time.RFC3339))
	req2 := httptest.NewRequest(http.MethodPost, "/api/captures", bytes.NewBufferString(cap2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+shop2.DevToken)
	rec2 := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("shop 2 capture failed: %d: %s", rec2.Code, rec2.Body.String())
	}

	rec2Body := fmt.Sprintf(`{
		"period_start": "%s",
		"period_end": "%s",
		"independent_source": "Paper Counter Log",
		"conducted_by": "Emeka & Shadow Auditor",
		"independent_records": [
			{
				"occurred_at": "%s",
				"sku_or_name": "MILO-500",
				"type": "sale",
				"quantity": 1,
				"unit": "pack",
				"amount": 250000
			}
		],
		"physical_stock_counts": [
			{ "sku_or_name": "MILO-500", "physical_count": 49 }
		]
	}`, now.Add(-1*time.Hour).Format(time.RFC3339), now.Format(time.RFC3339), t1.Format(time.RFC3339))

	rReq2 := httptest.NewRequest(http.MethodPost, "/api/pilot/reconcile", bytes.NewBufferString(rec2Body))
	rReq2.Header.Set("Content-Type", "application/json")
	rReq2.Header.Set("X-ListenLedger-Request", "1")
	rReq2.AddCookie(shop2.Cookie)
	rRec2 := httptest.NewRecorder()
	app.Handler.ServeHTTP(rRec2, rReq2)

	var recResp2 struct {
		Reconciliation storage.PilotReconciliation `json:"reconciliation"`
		AllGatesPassed bool                        `json:"all_gates_passed"`
	}
	_ = json.Unmarshal(rRec2.Body.Bytes(), &recResp2)

	if recResp2.Reconciliation.Status != "reconciled" {
		t.Fatalf("expected Shop 2 to be immediately reconciled, got %q", recResp2.Reconciliation.Status)
	}
	if !recResp2.AllGatesPassed {
		t.Fatalf("expected Shop 2 all gates passed")
	}

	// =========================================================================
	// PHASE 3: Expansion Readiness Gates (Toward 5 and 10–15 Shops)
	// =========================================================================
	evalReq3 := httptest.NewRequest(http.MethodPost, "/api/pilot/cohort/evaluate-readiness", bytes.NewBufferString(`{"promote":true}`))
	evalReq3.Header.Set("Content-Type", "application/json")
	evalReq3.Header.Set("X-ListenLedger-Request", "1")
	evalReq3.AddCookie(shop2.Cookie)
	evalRec3 := httptest.NewRecorder()
	app.Handler.ServeHTTP(evalRec3, evalReq3)

	var evalResp3 api.CohortReadinessResponse
	_ = json.Unmarshal(evalRec3.Body.Bytes(), &evalResp3)
	if evalResp3.ReadinessStatus != "qualified" {
		t.Fatalf("expected Shop 2 to qualify for expansion")
	}
	if evalResp3.NextPhase != "phase3_expanded_5" {
		t.Fatalf("expected next phase to be 'phase3_expanded_5', got %q", evalResp3.NextPhase)
	}
}
