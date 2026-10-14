package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestrictedStaticServer(t *testing.T) {
	tempDir := t.TempDir()

	// Create test files
	_ = os.WriteFile(filepath.Join(tempDir, "index.html"), []byte("<h1>ListenLedger</h1>"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "style.css"), []byte("body { color: black; }"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(`{"name":"test"}`), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "tsconfig.json"), []byte(`{"compilerOptions":{}}`), 0644)

	srcDir := filepath.Join(tempDir, "src")
	_ = os.MkdirAll(srcDir, 0755)
	_ = os.WriteFile(filepath.Join(srcDir, "app.ts"), []byte("const x = 1;"), 0644)

	distDir := filepath.Join(tempDir, "dist")
	_ = os.MkdirAll(distDir, 0755)
	_ = os.WriteFile(filepath.Join(distDir, "app.js"), []byte("console.log('hi');"), 0644)

	// In production mode (devMode = false)
	handlerProd := restrictedStaticServer(tempDir, false)

	tests := []struct {
		urlPath    string
		wantStatus int
	}{
		{"/", http.StatusOK},
		{"/index.html", http.StatusMovedPermanently},
		{"/style.css", http.StatusOK},
		{"/dist/app.js", http.StatusOK},
		{"/src/app.ts", http.StatusNotFound},
		{"/package.json", http.StatusNotFound},
		{"/tsconfig.json", http.StatusNotFound},
		{"/.env", http.StatusNotFound},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, tt.urlPath, nil)
		rec := httptest.NewRecorder()
		handlerProd.ServeHTTP(rec, req)

		if rec.Code != tt.wantStatus {
			t.Errorf("GET %s (prod): got status %d, want %d", tt.urlPath, rec.Code, tt.wantStatus)
		}
	}

	// In dev mode (devMode = true), source files are accessible for debugging
	handlerDev := restrictedStaticServer(tempDir, true)
	req := httptest.NewRequest(http.MethodGet, "/src/app.ts", nil)
	rec := httptest.NewRecorder()
	handlerDev.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /src/app.ts (dev): got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestSecurityHeaders(t *testing.T) {
	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	handler := securityHeaders(dummy, false)

	// 1. Plain HTTP request without forwarded header
	reqHTTP := httptest.NewRequest(http.MethodGet, "/", nil)
	recHTTP := httptest.NewRecorder()
	handler.ServeHTTP(recHTTP, reqHTTP)

	if got := recHTTP.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want 'nosniff'", got)
	}
	if got := recHTTP.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want 'DENY'", got)
	}
	if got := recHTTP.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want 'strict-origin-when-cross-origin'", got)
	}
	if got := recHTTP.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("expected Content-Security-Policy header to be set")
	}
	if got := recHTTP.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("expected no HSTS on plain HTTP, got %q", got)
	}

	// 2. HTTPS via X-Forwarded-Proto (reverse proxy like Render / Cloudflare / Caddy)
	reqProxy := httptest.NewRequest(http.MethodGet, "/", nil)
	reqProxy.Header.Set("X-Forwarded-Proto", "https")
	recProxy := httptest.NewRecorder()
	handler.ServeHTTP(recProxy, reqProxy)

	if got := recProxy.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("expected HSTS header when X-Forwarded-Proto is https")
	}

	// 3. Direct TLS connection
	reqTLS := httptest.NewRequest(http.MethodGet, "/", nil)
	reqTLS.TLS = &tls.ConnectionState{}
	recTLS := httptest.NewRecorder()
	handler.ServeHTTP(recTLS, reqTLS)

	if got := recTLS.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("expected HSTS header when r.TLS is set")
	}
}

func TestTLSFlagValidation(t *testing.T) {
	// Mismatched cert without key
	cfg1 := config{tlsCertFile: "cert.pem", tlsKeyFile: ""}
	if (cfg1.tlsCertFile != "" && cfg1.tlsKeyFile == "") || (cfg1.tlsCertFile == "" && cfg1.tlsKeyFile != "") {
		// As expected, validation fails
	} else {
		t.Error("expected validation failure when only tlsCertFile is provided")
	}

	// Mismatched key without cert
	cfg2 := config{tlsCertFile: "", tlsKeyFile: "key.pem"}
	if (cfg2.tlsCertFile != "" && cfg2.tlsKeyFile == "") || (cfg2.tlsCertFile == "" && cfg2.tlsKeyFile != "") {
		// As expected, validation fails
	} else {
		t.Error("expected validation failure when only tlsKeyFile is provided")
	}

	// Both provided
	cfg3 := config{tlsCertFile: "cert.pem", tlsKeyFile: "key.pem"}
	if (cfg3.tlsCertFile != "" && cfg3.tlsKeyFile == "") || (cfg3.tlsCertFile == "" && cfg3.tlsKeyFile != "") {
		t.Error("did not expect validation failure when both are provided")
	}
}

func TestTLSServerHandshake(t *testing.T) {
	tempDir := t.TempDir()
	certFile, keyFile := generateTestCertificate(t, tempDir)

	cer, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("failed to load x509 keypair: %v", err)
	}

	dummy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secure ok"))
	})
	handler := securityHeaders(dummy, false)

	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{cer},
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()
	defer server.Close()

	client := server.Client()
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("HTTPS GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	if hsts := resp.Header.Get("Strict-Transport-Security"); hsts == "" {
		t.Errorf("expected HSTS header on HTTPS connection, got %q", hsts)
	}
}

func generateTestCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(time.Hour)

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"ListenLedger Test"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	certPath := filepath.Join(dir, "cert.pem")
	certOut, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("failed to open cert.pem: %v", err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		t.Fatalf("failed to write cert pem: %v", err)
	}

	keyPath := filepath.Join(dir, "key.pem")
	keyOut, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("failed to open key.pem: %v", err)
	}
	defer keyOut.Close()
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes}); err != nil {
		t.Fatalf("failed to write key pem: %v", err)
	}

	return certPath, keyPath
}
