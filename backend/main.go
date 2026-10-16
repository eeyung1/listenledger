// Command listenledger-backend is the ListenLedger orchestrator: it owns the
// realtime channel to the owner's dashboard and the persisted catalogue and
// ledger behind it.
//
// The production image serves the dashboard and API against an initialized
// PostgreSQL database using a restricted runtime role.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/eeyung1/listenledger-backend/api"
	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/ws"
)

// shutdownGrace bounds how long in-flight requests have to finish once a
// shutdown signal arrives.
const shutdownGrace = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("backend stopped", "error", err)
		os.Exit(1)
	}
}

type config struct {
	addr            string
	dbPath          string
	frontendDir     string
	devMode         bool
	allowedOrigins  []string
	tlsCertFile     string
	tlsKeyFile      string
	requirePostgres bool
	skipMigrations  bool
}

func run() error {
	cfg := config{}

	frontendDefault := "../frontend"
	flag.StringVar(&cfg.addr, "addr", ":8080", "host:port to listen on")
	flag.StringVar(&cfg.dbPath, "db", "", "path to the SQLite database file or PostgreSQL connection URL (defaults to DATABASE_URL env or listenledger.db)")
	flag.StringVar(&cfg.frontendDir, "frontend", frontendDefault,
		"directory containing index.html and the built SPA")
	flag.BoolVar(&cfg.devMode, "dev", false,
		"enable development-only endpoints; never enable in production")
	flag.BoolVar(&cfg.requirePostgres, "require-postgres", false, "require a PostgreSQL database; refuse SQLite fallback")
	flag.BoolVar(&cfg.skipMigrations, "skip-migrations", false, "verify PostgreSQL schema without DDL; use a restricted runtime role")
	flag.StringVar(&cfg.tlsCertFile, "tls-cert", "", "path to TLS certificate PEM file (or LISTENLEDGER_TLS_CERT env)")
	flag.StringVar(&cfg.tlsKeyFile, "tls-key", "", "path to TLS private key PEM file (or LISTENLEDGER_TLS_KEY env)")
	origins := flag.String("allowed-origins", os.Getenv("ALLOWED_ORIGINS"),
		"comma-separated browser origins permitted to open a WebSocket (empty = same-origin only)")
	flag.Parse()

	// -skip-migrations is authoritative: runtime startup must never perform DDL.

	// Environment variable fallback for hosting platforms (Render, Supabase, Cloud Run, etc.)
	if cfg.dbPath == "" {
		if envDB := os.Getenv("DATABASE_URL"); envDB != "" {
			cfg.dbPath = envDB
		} else {
			cfg.dbPath = "listenledger.db"
		}
	}
	if err := validateDatabaseConfig(cfg); err != nil {
		return err
	}

	if port := os.Getenv("PORT"); port != "" && cfg.addr == ":8080" {
		if !strings.HasPrefix(port, ":") {
			cfg.addr = ":" + port
		} else {
			cfg.addr = port
		}
	}

	if cfg.tlsCertFile == "" {
		cfg.tlsCertFile = os.Getenv("LISTENLEDGER_TLS_CERT")
	}
	if cfg.tlsKeyFile == "" {
		cfg.tlsKeyFile = os.Getenv("LISTENLEDGER_TLS_KEY")
	}

	if (cfg.tlsCertFile != "" && cfg.tlsKeyFile == "") || (cfg.tlsCertFile == "" && cfg.tlsKeyFile != "") {
		return errors.New("both -tls-cert and -tls-key must be provided for HTTPS serving")
	}

	cfg.allowedOrigins = splitList(*origins)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	return serve(logger, cfg)
}

func redactDSN(dsn string) string {
	if !strings.Contains(dsn, "@") {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "[redacted-dsn]"
	}
	return u.Redacted()
}

func initStore(_ *slog.Logger, cfg config) (*storage.Store, error) {
	if cfg.skipMigrations {
		return storage.OpenPostgresRuntime(cfg.dbPath)
	}
	return storage.Open(cfg.dbPath)
}

func serve(logger *slog.Logger, cfg config) error {
	if err := validateDatabaseConfig(cfg); err != nil {
		return err
	}
	frontendDir, err := filepath.Abs(cfg.frontendDir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(frontendDir, "index.html")); err != nil {
		// Not fatal: the API is still useful headlessly, and the operator may
		// simply have started the binary from the wrong directory. Say so
		// plainly instead of serving 404s that look like a client bug.
		logger.Warn("frontend directory does not look like a built SPA",
			"path", frontendDir, "error", err)
	}

	store, err := initStore(logger, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Error("closing store", "error", err)
		}
	}()
	logger.Info("storage ready", "dialect", store.Dialect(), "target", redactDSN(cfg.dbPath))

	hub := ws.NewHub(logger)
	go hub.Run()
	defer hub.Shutdown()

	mux := http.NewServeMux()

	// Both surfaces resolve the shop through the same seam, so they cannot
	// disagree about who a caller is. Swapping auth.QueryShopResolver for a
	// session-backed resolver changes them together.
	identity := &auth.Service{Store: store, SecureCookies: !cfg.devMode}
	mux.Handle("/api/auth/", identity.Handler())
	mux.Handle("/ws", ws.NewHandler(hub, identity.ResolveShop, cfg.allowedOrigins, logger))
	mux.Handle("/api/", auth.ProtectWrites(api.New(store, hub, identity.ResolveShop, logger)))

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "ok",
			"ws_clients":        hub.ClientCount(),
			"ws_events_dropped": hub.DroppedCount(),
		})
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := store.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":   "unavailable",
				"database": "unreachable",
				"error":    err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":            "ready",
			"database":          "connected",
			"ws_clients":        hub.ClientCount(),
			"ws_events_dropped": hub.DroppedCount(),
		})
	})

	if cfg.devMode {
		logger.Warn("development endpoints enabled; do not run this way in production")
		mux.Handle("/api/dev/broadcast", devBroadcast(hub, logger))
		// Wipes the named shop before inserting a fixed dataset. Destructive
		// and unauthenticated: -dev only, same as the broadcast above.
		mux.Handle("/api/dev/seed", api.NewDevSeeder(store, hub, logger))
	}

	// The SPA is served from the same origin as /ws, which is what lets the
	// default same-origin WebSocket policy apply without configuration.
	//
	// In production, static serving is restricted to built assets (HTML, CSS, JS, etc.)
	// and raw TypeScript sources or build configurations are blocked.
	mux.Handle("/", noStoreCache(restrictedStaticServer(frontendDir, cfg.devMode)))

	rootHandler := api.Recoverer(logger)(api.RequestIDMiddleware(securityHeaders(mux, cfg.devMode)))

	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           rootHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// WriteTimeout must stay zero: it is a deadline on the whole
		// connection, and a WebSocket response is never "finished". A
		// non-zero value here kills live dashboards mid-stream. Per-message
		// deadlines inside the ws package are what bound writes instead.
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
		ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	if cfg.tlsCertFile != "" && cfg.tlsKeyFile != "" {
		server.TLSConfig = &tls.Config{
			MinVersion:               tls.VersionTLS12,
			PreferServerCipherSuites: true,
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listenErr := make(chan error, 1)
	go func() {
		if cfg.tlsCertFile != "" && cfg.tlsKeyFile != "" {
			logger.Info("listening with TLS", "addr", cfg.addr, "frontend", frontendDir, "cert", cfg.tlsCertFile)
			if err := server.ListenAndServeTLS(cfg.tlsCertFile, cfg.tlsKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				listenErr <- err
			}
		} else {
			logger.Info("listening", "addr", cfg.addr, "frontend", frontendDir, "dev_mode", cfg.devMode)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				listenErr <- err
			}
		}
	}()

	select {
	case err := <-listenErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// http.Server.Shutdown waits for in-flight requests but not for hijacked
	// connections, and every WebSocket is hijacked. Closing the hub first is
	// what actually disconnects the dashboards.
	hub.Shutdown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}

	logger.Info("stopped cleanly")
	return nil
}

func validateDatabaseConfig(cfg config) error {
	postgres := strings.HasPrefix(cfg.dbPath, "postgres://") || strings.HasPrefix(cfg.dbPath, "postgresql://")
	if (cfg.requirePostgres || cfg.skipMigrations) && !postgres {
		return errors.New("PostgreSQL is required: set DATABASE_URL to a PostgreSQL connection URL; SQLite fallback is disabled")
	}
	return nil
}

// devBroadcast publishes a caller-supplied event, which is how the realtime
// pipeline is exercised before any capture or storage code exists.
//
//	curl -X POST localhost:8080/api/dev/broadcast \
//	  -d '{"shop_id":"shop-1","type":"inventory.updated","data":{"sku":"PEAK-400"}}'
//
// DEVELOPMENT ONLY: it lets anyone inject events into any shop.
func devBroadcast(hub *ws.Hub, logger *slog.Logger) http.HandlerFunc {
	type request struct {
		ShopID string          `json:"shop_id"`
		Type   string          `json:"type"`
		Data   json.RawMessage `json:"data"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var body request
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		eventType := ws.EventType(body.Type)
		if eventType == "" {
			eventType = ws.EventTransactionCreated
		}

		var payload any
		if len(body.Data) > 0 {
			payload = body.Data
		}

		accepted := hub.Publish(body.ShopID, eventType, payload)
		logger.Warn("dev: synthetic event published",
			"type", eventType, "shop_id", body.ShopID, "accepted", accepted)

		status := http.StatusAccepted
		if !accepted {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]any{
			"accepted": accepted,
			"clients":  hub.ClientCount(),
		})
	}
}

// noStoreCache stops the browser caching the shell and templates while the
// dashboard is under active development.
func noStoreCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// restrictedStaticServer serves static assets from frontendDir while blocking access
// to uncompiled TypeScript source files and build configurations in production.
func restrictedStaticServer(frontendDir string, devMode bool) http.Handler {
	fs := http.FileServer(http.Dir(frontendDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(r.URL.Path)

		// Never serve hidden files (.git, .env, etc.)
		for _, part := range strings.Split(clean, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") && part != "." && part != ".." {
				http.NotFound(w, r)
				return
			}
		}

		if !devMode {
			ext := strings.ToLower(filepath.Ext(clean))
			base := strings.ToLower(filepath.Base(clean))
			// Block source files, lockfiles, and configuration files in production mode
			if ext == ".ts" || ext == ".tsx" || ext == ".go" ||
				base == "package.json" || base == "package-lock.json" ||
				base == "tsconfig.json" || strings.Contains(clean, "node_modules") {
				http.NotFound(w, r)
				return
			}
		}

		fs.ServeHTTP(w, r)
	})
}

// securityHeaders adds modern defensive HTTP response headers (CSP, HSTS, frame protection, MIME protection).
func securityHeaders(next http.Handler, devMode bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:; font-src 'self'; frame-ancestors 'none';")

		// Apply HSTS when served over TLS directly or behind a TLS-terminating reverse proxy (e.g. Render, Caddy, Cloudflare)
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}

		next.ServeHTTP(w, r)
	})
}
