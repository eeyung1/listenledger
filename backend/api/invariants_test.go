package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
)

func TestReadinessEndpoint(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := store.Ping(ctx); err != nil {
		t.Fatalf("expected store.Ping to succeed, got %v", err)
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(3, 500*time.Millisecond)

	key := "test_client_1"
	if !limiter.Allow(key) {
		t.Fatal("expected request 1 to be allowed")
	}
	if !limiter.Allow(key) {
		t.Fatal("expected request 2 to be allowed")
	}
	if !limiter.Allow(key) {
		t.Fatal("expected request 3 to be allowed")
	}
	if limiter.Allow(key) {
		t.Fatal("expected request 4 to be rejected under rate limit")
	}

	// Another key should still be allowed
	if !limiter.Allow("test_client_2") {
		t.Fatal("expected different key to be allowed")
	}

	// After window expires, original key should be allowed again
	time.Sleep(550 * time.Millisecond)
	if !limiter.Allow(key) {
		t.Fatal("expected request to be allowed after window reset")
	}
}

func TestTransactionInvariantGuards(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	shopID := "shop_invariants"
	if err := store.CreateAccount(context.Background(), storage.Account{
		Identity: storage.Identity{
			OwnerID:  "owner_inv",
			Email:    "owner@inv.com",
			ShopID:   shopID,
			ShopName: "Invariant Shop",
			Currency: "NGN",
		},
		PasswordHash: "dummyhash",
		RecoveryHash: "dummyrecovery",
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}

	activeProd := models.Product{
		ID:       "prd_act_1",
		ShopID:   shopID,
		Name:     "Active Product",
		SKU:      "ACT-001",
		BaseUnit: "pack",
		Price:    models.Money{Amount: 100000, Currency: "NGN"},
		Quantity: 50,
	}
	if err := store.InsertProduct(context.Background(), activeProd); err != nil {
		t.Fatalf("insert product: %v", err)
	}

	archivedProd := models.Product{
		ID:       "prd_arc_1",
		ShopID:   shopID,
		Name:     "Archived Product",
		SKU:      "ARC-001",
		BaseUnit: "pack",
		Price:    models.Money{Amount: 100000, Currency: "NGN"},
		Quantity: 50,
	}
	if err := store.InsertProduct(context.Background(), archivedProd); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if _, err := store.ArchiveProduct(context.Background(), shopID, archivedProd.ID); err != nil {
		t.Fatalf("archive product: %v", err)
	}

	resolveShop := func(r *http.Request) (string, error) {
		return shopID, nil
	}
	handler := New(store, nil, resolveShop, nil)

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
	}{
		{
			name: "Nonexistent product_id rejected",
			body: map[string]any{
				"type":         "sale",
				"product_id":   "prd_nonexistent_999",
				"product_name": "Ghost Item",
				"quantity":     1,
				"unit":         "pack",
				"amount":       map[string]any{"amount": 100000, "currency": "NGN"},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Archived product_id rejected",
			body: map[string]any{
				"type":         "sale",
				"product_id":   archivedProd.ID,
				"product_name": archivedProd.Name,
				"quantity":     1,
				"unit":         "pack",
				"amount":       map[string]any{"amount": 100000, "currency": "NGN"},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Zero or negative quantity rejected",
			body: map[string]any{
				"type":         "sale",
				"product_id":   activeProd.ID,
				"product_name": activeProd.Name,
				"quantity":     0,
				"unit":         "pack",
				"amount":       map[string]any{"amount": 100000, "currency": "NGN"},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Implausibly large quantity rejected",
			body: map[string]any{
				"type":         "sale",
				"product_id":   activeProd.ID,
				"product_name": activeProd.Name,
				"quantity":     500000,
				"unit":         "pack",
				"amount":       map[string]any{"amount": 100000, "currency": "NGN"},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative amount rejected",
			body: map[string]any{
				"type":         "sale",
				"product_id":   activeProd.ID,
				"product_name": activeProd.Name,
				"quantity":     1,
				"unit":         "pack",
				"amount":       map[string]any{"amount": -500, "currency": "NGN"},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid currency format rejected",
			body: map[string]any{
				"type":         "sale",
				"product_id":   activeProd.ID,
				"product_name": activeProd.Name,
				"quantity":     1,
				"unit":         "pack",
				"amount":       map[string]any{"amount": 100000, "currency": "1234"},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Confidence score out of bounds (> 1.0) rejected",
			body: map[string]any{
				"type":         "sale",
				"product_id":   activeProd.ID,
				"product_name": activeProd.Name,
				"quantity":     1,
				"unit":         "pack",
				"amount":       map[string]any{"amount": 100000, "currency": "NGN"},
				"confidence":   map[string]any{"overall": 1.5},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Valid active product transaction accepted",
			body: map[string]any{
				"type":         "sale",
				"product_id":   activeProd.ID,
				"product_name": activeProd.Name,
				"quantity":     2,
				"unit":         "pack",
				"amount":       map[string]any{"amount": 200000, "currency": "NGN"},
				"confidence":   map[string]any{"overall": 0.95, "product": 0.95, "quantity": 0.95, "amount": 0.95},
			},
			wantStatus: http.StatusCreated,
		},
	}

	for idx, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "idemp_inv_"+string(rune(idx+'A')))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d. Body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}
