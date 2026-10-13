package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
