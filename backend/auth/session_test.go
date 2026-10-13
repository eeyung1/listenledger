package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
	"github.com/gorilla/websocket"
	"strings"
)

func TestOwnerSessionsIsolationRecovery(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := &auth.Service{Store: store, SecureCookies: true}
	hub := ws.NewHub(nil)
	go hub.Run()
	defer hub.Shutdown()
	mux := http.NewServeMux()
	mux.Handle("/api/auth/", service.Handler())
	mux.Handle("/api/", auth.ProtectWrites(api.New(store, hub, service.ResolveShop, nil)))
	mux.Handle("/ws", ws.NewHandler(hub, service.ResolveShop, nil, nil))
	server := httptest.NewServer(mux)
	defer server.Close()
	call := func(path string, body any, cookie *http.Cookie, header bool) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(data))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if header {
			r.Header.Set("X-ListenLedger-Request", "1")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	signup := func(email string) (*http.Cookie, map[string]any) {
		t.Helper()
		w := call("/api/auth/signup", map[string]string{"email": email, "password": "correct horse battery", "shop_name": "Pilot shop"}, nil, true)
		if w.Code != 200 {
			t.Fatalf("signup: %d %s", w.Code, w.Body)
		}
		var body map[string]any
		json.Unmarshal(w.Body.Bytes(), &body)
		cookie := w.Result().Cookies()[0]
		if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatal("insecure cookie")
		}
		return cookie, body
	}
	a, one := signup("owner1@example.com")
	b, two := signup("owner2@example.com")
	get := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := get("/api/inventory?shop_id="+one["shop_id"].(string), nil); w.Code != 401 {
		t.Fatalf("anonymous read: %d", w.Code)
	}
	if w := get("/api/inventory?shop_id="+one["shop_id"].(string), b); w.Code != 401 {
		t.Fatalf("cross shop read: %d", w.Code)
	}
	if w := get("/api/inventory", a); w.Code != 200 {
		t.Fatalf("owner read: %d", w.Code)
	}
	product := map[string]any{"name": "Milo", "sku": "MILO", "base_unit": "pack", "quantity": 20, "price": map[string]any{"amount": 200000, "currency": "NGN"}}
	if w := call("/api/products", product, a, false); w.Code != 403 {
		t.Fatal("missing CSRF header accepted")
	}
	if w := call("/api/products?shop_id="+one["shop_id"].(string), product, b, true); w.Code != 401 {
		t.Fatal("cross shop write accepted")
	}
	if w := call("/api/products", product, a, true); w.Code != 201 {
		t.Fatalf("product: %s", w.Body)
	}
	if w := get("/api/inventory", b); strings.Contains(w.Body.String(), "Milo") {
		t.Fatal("product leaked")
	}
	// WebSocket identity cannot be selected by another shop's query string.
	header := http.Header{}
	header.Set("Cookie", a.String())
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	c, resp, err := websocket.DefaultDialer.Dial(url+"?shop_id="+two["shop_id"].(string), header)
	if err == nil {
		c.Close()
		t.Fatal("cross shop websocket accepted")
	}
	if resp.StatusCode != 401 {
		t.Fatal(resp.StatusCode)
	}
	c, _, err = websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	recovery := map[string]string{"email": "owner1@example.com", "password": "replacement password strong", "recovery_code": one["recovery_code"].(string)}
	w := call("/api/auth/recover", recovery, nil, true)
	if w.Code != 200 {
		t.Fatalf("recovery: %s", w.Body)
	}
	if get("/api/inventory", a).Code != 401 {
		t.Fatal("old session survived recovery")
	}
	if call("/api/auth/recover", recovery, nil, true).Code != 401 {
		t.Fatal("recovery code reused")
	}
	hub.Publish(one["shop_id"].(string), ws.EventProductCreated, map[string]string{"secret": "must not arrive"})
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err = c.ReadMessage(); err == nil {
		t.Fatal("revoked socket received data")
	}
	newCookie := w.Result().Cookies()[0]
	if call("/api/auth/logout", nil, newCookie, true).Code != 200 || get("/api/inventory", newCookie).Code != 401 {
		t.Fatal("logout did not revoke session")
	}
	if call("/api/auth/login", map[string]string{"email": "owner1@example.com", "password": "correct horse battery"}, nil, true).Code != 401 {
		t.Fatal("old password still works")
	}
	if call("/api/auth/login", map[string]string{"email": "owner1@example.com", "password": "replacement password strong"}, nil, true).Code != 200 {
		t.Fatal("new password failed")
	}
}
func TestOriginAndPersistentThrottle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, e := storage.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	service := &auth.Service{Store: s}
	r := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBufferString(`{"email":"a@example.com","password":"long enough password"}`))
	r.Header.Set("X-ListenLedger-Request", "1")
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	service.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin accepted")
	}
	for i := 0; i < 10; i++ {
		ok, e := s.AllowAuthAttempt(r.Context(), "account", 10)
		if e != nil || !ok {
			t.Fatal(i, e)
		}
	}
	s.Close()
	s, e = storage.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if ok, e := s.AllowAuthAttempt(r.Context(), "account", 10); e != nil || ok {
		t.Fatal("throttle reset after reopen", e)
	}
}
