package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

func setupDrillTestApp(t *testing.T) (*storage.Store, http.Handler, string) {
	dbPath := filepath.Join(t.TempDir(), "drill_test.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	shopID := "shop_drill_test_01"
	ctx := t.Context()

	err = store.CreateAccount(ctx, storage.Account{
		Identity: storage.Identity{
			OwnerID:  "own_drill_01",
			Email:    "drill@mamadolu.ng",
			ShopID:   shopID,
			ShopName: "Mama Tolu Provisions",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "secure_pass_hash",
		RecoveryHash: "secure_recovery_hash",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	// Seed product
	err = store.InsertProduct(ctx, models.Product{
		ID:                "prd_milo_500",
		ShopID:            shopID,
		Name:              "Milo 500g",
		SKU:               "MILO-500",
		Aliases:           []string{"Milo", "Big Milo", "Nestle Milo"},
		Price:             models.Money{Amount: 250000, Currency: "NGN"},
		Quantity:          50,
		BaseUnit:          models.UnitPack,
		LowStockThreshold: 10,
		Packaging: []models.PackagingRelation{
			{From: models.UnitCarton, To: models.UnitPack, Factor: 12},
		},
	})
	if err != nil {
		t.Fatalf("seed product: %v", err)
	}

	resolver := func(_ *http.Request) (string, error) {
		return shopID, nil
	}
	handler := api.New(store, nil, resolver, nil)
	return store, handler, shopID
}

func TestCashierProtocolDrill_CleanSale(t *testing.T) {
	_, handler, _ := setupDrillTestApp(t)

	body := `{"scenario_type":"clean_sale","transcript":"Two packs Milo, five thousand naira. Thank you."}`
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/drill", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Passed           bool   `json:"passed"`
		Score            int    `json:"score"`
		ScenarioType     string `json:"scenario_type"`
		BoundaryDetected bool   `json:"boundary_detected"`
		ClosingPhrase    string `json:"closing_phrase"`
		RecognizedItem   string `json:"recognized_item"`
		Checklist        []struct {
			Name   string `json:"name"`
			Passed bool   `json:"passed"`
			Detail string `json:"detail"`
		} `json:"checklist"`
		CoachingTips   []string `json:"coaching_tips"`
		StaffTrainedAt string   `json:"staff_trained_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !res.Passed {
		t.Errorf("expected clean sale drill to pass")
	}
	if res.Score != 100 {
		t.Errorf("expected score 100, got %d", res.Score)
	}
	if !res.BoundaryDetected || res.ClosingPhrase != "Thank you" {
		t.Errorf("expected boundary 'Thank you', got detected=%v phrase=%q", res.BoundaryDetected, res.ClosingPhrase)
	}
	if res.RecognizedItem != "Milo 500g" {
		t.Errorf("expected recognized item 'Milo 500g', got %q", res.RecognizedItem)
	}
	if res.StaffTrainedAt == "" {
		t.Errorf("expected staff_trained_at timestamp to be set on pass")
	}
}

func TestCashierProtocolDrill_Restock(t *testing.T) {
	_, handler, _ := setupDrillTestApp(t)

	body := `{"scenario_type":"restock","transcript":"Milo two cartons thirty thousand naira. Stock received."}`
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/drill", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Passed           bool   `json:"passed"`
		Score            int    `json:"score"`
		BoundaryDetected bool   `json:"boundary_detected"`
		ClosingPhrase    string `json:"closing_phrase"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !res.Passed {
		t.Errorf("expected restock drill to pass")
	}
	if !res.BoundaryDetected || res.ClosingPhrase != "Stock received" {
		t.Errorf("expected boundary 'Stock received', got %q", res.ClosingPhrase)
	}
}

func TestCashierProtocolDrill_NonCommercialChatter(t *testing.T) {
	_, handler, _ := setupDrillTestApp(t)

	// 1. Casual banter WITHOUT closing phrase -> Passes negative test!
	goodChatter := `{"scenario_type":"non_commercial_chatter","transcript":"Good morning my brother how is the family today?"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/onboarding/drill", strings.NewReader(goodChatter))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	var res1 struct {
		Passed           bool `json:"passed"`
		Score            int  `json:"score"`
		BoundaryDetected bool `json:"boundary_detected"`
	}
	if err := json.NewDecoder(rec1.Body).Decode(&res1); err != nil {
		t.Fatalf("decode response 1: %v", err)
	}

	if !res1.Passed || res1.BoundaryDetected {
		t.Errorf("expected chatter without closing phrase to pass negative test, got passed=%v boundary=%v", res1.Passed, res1.BoundaryDetected)
	}

	// 2. Casual chatter WITH accidental closing phrase -> Fails drill with warning!
	badChatter := `{"scenario_type":"non_commercial_chatter","transcript":"See you tomorrow my brother, thank you."}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/onboarding/drill", strings.NewReader(badChatter))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	var res2 struct {
		Passed           bool     `json:"passed"`
		Score            int      `json:"score"`
		BoundaryDetected bool     `json:"boundary_detected"`
		CoachingTips     []string `json:"coaching_tips"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&res2); err != nil {
		t.Fatalf("decode response 2: %v", err)
	}

	if res2.Passed || !res2.BoundaryDetected {
		t.Errorf("expected chatter with closing phrase to fail negative test, got passed=%v boundary=%v", res2.Passed, res2.BoundaryDetected)
	}
	if len(res2.CoachingTips) == 0 {
		t.Errorf("expected coaching tip warning against using closing phrases in idle talk")
	}
}

func TestCashierProtocolDrill_SpokenCorrection(t *testing.T) {
	_, handler, _ := setupDrillTestApp(t)

	body := `{"scenario_type":"spoken_correction","transcript":"Give me 3 packs Milo no wait make that 2 packs five thousand naira Thank you."}`
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/drill", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Passed bool `json:"passed"`
		Score  int  `json:"score"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !res.Passed {
		t.Errorf("expected self-correction drill to pass")
	}
}

func TestTargetGates_APIEndpoints(t *testing.T) {
	_, handler, _ := setupDrillTestApp(t)

	// 1. GET /api/settings/target-gates returns default targets with is_agreed = false
	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/target-gates", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var getRes struct {
		Gates    models.TargetGates `json:"gates"`
		IsAgreed bool               `json:"is_agreed"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&getRes); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if getRes.IsAgreed {
		t.Errorf("expected is_agreed = false initially")
	}
	if getRes.Gates.MinFullTransactionAccuracy != 90.0 {
		t.Errorf("expected 90%% accuracy default, got %.1f%%", getRes.Gates.MinFullTransactionAccuracy)
	}

	// 2. PUT /api/settings/target-gates updates and agrees targets
	putBody := `{"min_full_transaction_accuracy":91.0,"min_sku_accuracy":96.0,"min_quantity_accuracy":95.5,"min_unit_accuracy":95.0,"min_amount_accuracy":95.0,"max_false_capture_rate":2.5,"max_missed_transaction_rate":4.5,"max_correction_rate":9.0,"min_protocol_compliance_rate":88.0,"max_p95_latency_ms":1400,"agreed_by":"Shop Owner pilot@mamadolu.ng"}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings/target-gates", strings.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)

	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on put, got %d: %s", putRec.Code, putRec.Body.String())
	}

	var putRes struct {
		Gates    models.TargetGates `json:"gates"`
		IsAgreed bool               `json:"is_agreed"`
	}
	if err := json.NewDecoder(putRec.Body).Decode(&putRes); err != nil {
		t.Fatalf("decode put response: %v", err)
	}
	if !putRes.IsAgreed {
		t.Errorf("expected is_agreed = true after PUT")
	}
	if putRes.Gates.MinFullTransactionAccuracy != 91.0 {
		t.Errorf("expected 91.0%%, got %.1f%%", putRes.Gates.MinFullTransactionAccuracy)
	}
	if putRes.Gates.AgreedBy != "Shop Owner pilot@mamadolu.ng" {
		t.Errorf("expected AgreedBy set, got %q", putRes.Gates.AgreedBy)
	}

	// 3. PUT with invalid values returns 400 Bad Request
	badPutBody := `{"min_full_transaction_accuracy":150.0,"min_sku_accuracy":96.0,"max_p95_latency_ms":1400}`
	badPutReq := httptest.NewRequest(http.MethodPut, "/api/settings/target-gates", strings.NewReader(badPutBody))
	badPutReq.Header.Set("Content-Type", "application/json")
	badPutRec := httptest.NewRecorder()
	handler.ServeHTTP(badPutRec, badPutReq)

	if badPutRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for rate > 100%%, got %d", badPutRec.Code)
	}
}
