package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

func setupOnboardingTestApp(t *testing.T) (*storage.Store, http.Handler, string) {
	t.Helper()
	store, err := storage.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	testID := time.Now().Format("20060102150405.000000000")
	shopID := "shop_onboard_" + testID
	err = store.CreateAccount(context.Background(), storage.Account{
		Identity: storage.Identity{
			OwnerID:  "owner_" + testID,
			Email:    "owner_" + testID + "@example.com",
			ShopID:   shopID,
			ShopName: "Onboarding Shop",
			Currency: "NGN",
			Timezone: "Africa/Lagos",
		},
		PasswordHash: "dummyhash",
		RecoveryHash: "dummyrecovery",
	})
	if err != nil {
		t.Fatalf("failed to create test account: %v", err)
	}

	handler := api.New(store, nil, func(r *http.Request) (string, error) {
		return shopID, nil
	}, nil)

	return store, handler, shopID
}

func TestOnboardingStatusProgression(t *testing.T) {
	store, handler, shopID := setupOnboardingTestApp(t)
	_ = store
	_ = shopID

	// Step 1 initial state: profile complete, but no catalogue, no devices, no test capture
	req := httptest.NewRequest(http.MethodGet, "/api/onboarding/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var statusRes struct {
		ShopID         string `json:"shop_id"`
		CompletedSteps int    `json:"completed_steps"`
		TotalSteps     int    `json:"total_steps"`
		IsComplete     bool   `json:"is_complete"`
		Steps          struct {
			Profile struct {
				Completed bool `json:"completed"`
			} `json:"profile"`
			Catalogue struct {
				Completed bool `json:"completed"`
			} `json:"catalogue"`
			Devices struct {
				Completed bool `json:"completed"`
			} `json:"devices"`
			TestCapture struct {
				Completed bool `json:"completed"`
			} `json:"test_capture"`
		} `json:"steps"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&statusRes); err != nil {
		t.Fatalf("decode onboarding status: %v", err)
	}

	if !statusRes.Steps.Profile.Completed {
		t.Errorf("expected profile step completed")
	}
	if statusRes.Steps.Catalogue.Completed {
		t.Errorf("expected catalogue step incomplete")
	}
	if statusRes.Steps.Devices.Completed {
		t.Errorf("expected devices step incomplete")
	}
	if statusRes.Steps.TestCapture.Completed {
		t.Errorf("expected test capture step incomplete")
	}
	if statusRes.IsComplete {
		t.Errorf("expected onboarding not yet complete")
	}

	// Step 2: Add product
	prodBody := `{"name":"Peak Milk 400g","sku":"PEAK-400","base_unit":"piece","price":{"amount":350000,"currency":"NGN"},"quantity":24,"aliases":["Peak","Big Peak"]}`
	prodReq := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(prodBody))
	prodReq.Header.Set("Content-Type", "application/json")
	prodRec := httptest.NewRecorder()
	handler.ServeHTTP(prodRec, prodReq)
	if prodRec.Code != http.StatusCreated {
		t.Fatalf("add product failed: %d %s", prodRec.Code, prodRec.Body.String())
	}

	// Step 3: Pair a device
	devBody := `{"name":"Counter Pi 4"}`
	devReq := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(devBody))
	devReq.Header.Set("Content-Type", "application/json")
	devRec := httptest.NewRecorder()
	handler.ServeHTTP(devRec, devReq)
	if devRec.Code != http.StatusCreated {
		t.Fatalf("pair device failed: %d %s", devRec.Code, devRec.Body.String())
	}

	// Check status: 3 steps completed
	req2 := httptest.NewRequest(http.MethodGet, "/api/onboarding/status", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	_ = json.NewDecoder(rec2.Body).Decode(&statusRes)
	if statusRes.CompletedSteps != 3 {
		t.Fatalf("expected 3 completed steps, got %d", statusRes.CompletedSteps)
	}

	// Step 4: Run test capture
	tcBody := `{"transcript":"Two Peak Milk, seven thousand naira. Thank you.","type":"sale"}`
	tcReq := httptest.NewRequest(http.MethodPost, "/api/onboarding/test-capture", strings.NewReader(tcBody))
	tcReq.Header.Set("Content-Type", "application/json")
	tcRec := httptest.NewRecorder()
	handler.ServeHTTP(tcRec, tcReq)

	if tcRec.Code != http.StatusOK {
		t.Fatalf("test capture failed: %d %s", tcRec.Code, tcRec.Body.String())
	}

	var tcRes struct {
		Status           string `json:"status"`
		Recognized       bool   `json:"recognized"`
		BoundaryDetected bool   `json:"boundary_detected"`
		Summary          string `json:"summary"`
		Lines            []struct {
			ProductName string `json:"product_name"`
			MatchedBy   string `json:"matched_by"`
			Quantity    int    `json:"quantity"`
		} `json:"lines"`
	}
	if err := json.NewDecoder(tcRec.Body).Decode(&tcRes); err != nil {
		t.Fatalf("decode test capture response: %v", err)
	}

	if !tcRes.Recognized {
		t.Errorf("expected test capture to be recognized")
	}
	if !tcRes.BoundaryDetected {
		t.Errorf("expected boundary detected")
	}
	if len(tcRes.Lines) == 0 || tcRes.Lines[0].ProductName != "Peak Milk 400g" {
		t.Errorf("expected Peak Milk 400g recognized, got %+v", tcRes.Lines)
	}

	// Verify all 4 steps are complete
	req3 := httptest.NewRequest(http.MethodGet, "/api/onboarding/status", nil)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	_ = json.NewDecoder(rec3.Body).Decode(&statusRes)

	if !statusRes.IsComplete || statusRes.CompletedSteps != 4 {
		t.Errorf("expected onboarding complete (4/4), got %d/4 (is_complete=%v)", statusRes.CompletedSteps, statusRes.IsComplete)
	}
}

func TestDuplicateSKUAndAmbiguousAliasValidation(t *testing.T) {
	_, handler, _ := setupOnboardingTestApp(t)

	// Create initial product
	p1 := `{"name":"Peak Milk 400g","sku":"PEAK-400","base_unit":"piece","price":{"amount":350000,"currency":"NGN"},"quantity":10,"aliases":["Peak","Big Peak"]}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(p1))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("initial product creation failed: %d %s", rec1.Code, rec1.Body.String())
	}

	// 1. Duplicate SKU must be rejected with 409 Conflict
	dupSKU := `{"name":"Peak Milk Sachet","sku":"PEAK-400","base_unit":"piece","price":{"amount":100000,"currency":"NGN"},"quantity":10}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(dupSKU))
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict for duplicate SKU, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Case-insensitive duplicate SKU
	caseSKU := `{"name":"Peak Milk Sachet","sku":"peak-400","base_unit":"piece","price":{"amount":100000,"currency":"NGN"},"quantity":10}`
	req3 := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(caseSKU))
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict for case-insensitive duplicate SKU, got %d: %s", rec3.Code, rec3.Body.String())
	}

	// 2. Duplicate exact product name
	dupName := `{"name":"Peak Milk 400g","sku":"PEAK-OTHER","base_unit":"piece","price":{"amount":100000,"currency":"NGN"},"quantity":10}`
	req4 := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(dupName))
	rec4 := httptest.NewRecorder()
	handler.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict for duplicate product name, got %d: %s", rec4.Code, rec4.Body.String())
	}

	// 3. Ambiguous alias collision (using an alias already assigned to Peak Milk)
	dupAlias := `{"name":"Carnation Milk","sku":"CARN-100","base_unit":"piece","price":{"amount":200000,"currency":"NGN"},"quantity":5,"aliases":["Big Peak"]}`
	req5 := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(dupAlias))
	rec5 := httptest.NewRecorder()
	handler.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict for ambiguous alias, got %d: %s", rec5.Code, rec5.Body.String())
	}

	// 4. Duplicate alias within own product definition
	selfDup := `{"name":"Milo 500g","sku":"MILO-500","base_unit":"piece","price":{"amount":400000,"currency":"NGN"},"quantity":5,"aliases":["Milo Refill","Milo Refill"]}`
	req6 := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(selfDup))
	rec6 := httptest.NewRecorder()
	handler.ServeHTTP(rec6, req6)
	if rec6.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict for self-duplicate alias, got %d: %s", rec6.Code, rec6.Body.String())
	}
}

func TestPreventBaseUnitMutationWithHistoricalMovements(t *testing.T) {
	store, handler, shopID := setupOnboardingTestApp(t)

	// Create product with base unit "piece"
	p := `{"name":"Milo 500g","sku":"MILO-500","base_unit":"piece","price":{"amount":400000,"currency":"NGN"},"quantity":20}`
	req := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(p))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create product failed: %d %s", rec.Code, rec.Body.String())
	}

	var created models.Product
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created product: %v", err)
	}

	// Record a manual sale transaction to establish historical stock movements
	manualBody := fmt.Sprintf(`{"type":"sale","product_id":"%s","quantity":2,"unit":"piece","amount":{"amount":800000,"currency":"NGN"}}`, created.ID)
	mReq := httptest.NewRequest(http.MethodPost, "/api/transactions/manual", strings.NewReader(manualBody))
	mRec := httptest.NewRecorder()
	handler.ServeHTTP(mRec, mReq)
	if mRec.Code != http.StatusCreated {
		t.Fatalf("record manual transaction failed: %d %s", mRec.Code, mRec.Body.String())
	}

	// Verify movement exists
	movements, total, err := store.GetProductMovements(context.Background(), shopID, created.ID, 10, 0)
	if err != nil || total == 0 || len(movements) == 0 {
		t.Fatalf("expected movements to exist, got %d (err: %v)", total, err)
	}

	// Attempt to change base_unit from "piece" to "carton"
	patchBody := `{"base_unit":"carton"}`
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/products/"+created.ID, bytes.NewBufferString(patchBody))
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patchReq)

	if patchRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when attempting to mutate base_unit with history, got %d: %s", patchRec.Code, patchRec.Body.String())
	}

	if !strings.Contains(patchRec.Body.String(), "historical stock movements") {
		t.Errorf("expected error message mentioning historical movements, got %s", patchRec.Body.String())
	}

	// Updating other attributes without changing base_unit should succeed
	safePatch := `{"low_stock_threshold":5}`
	safeReq := httptest.NewRequest(http.MethodPatch, "/api/products/"+created.ID, bytes.NewBufferString(safePatch))
	safeRec := httptest.NewRecorder()
	handler.ServeHTTP(safeRec, safeReq)

	if safeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for safe attribute update, got %d: %s", safeRec.Code, safeRec.Body.String())
	}
}
