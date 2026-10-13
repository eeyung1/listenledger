// Package api serves the REST surface the dashboard reads.
//
// These endpoints exist for two moments in the SPA's life: first paint, and
// recovery after the WebSocket stream has been interrupted. Both need the same
// thing — a complete, consistent snapshot of a shop's state as of a known
// instant — which is why every response carries generated_at and why nothing
// here is incremental.
//
// Everything is read-only for now. Writes arrive with the capture endpoint and
// the Review Queue actions.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/auth"
	"github.com/eeyung1/listenledger-backend/models"
	"github.com/eeyung1/listenledger-backend/storage"
	"github.com/eeyung1/listenledger-backend/validate"
	"github.com/eeyung1/listenledger-backend/ws"
)

// Publisher broadcasts an event to the dashboards of one shop.
//
// It is an interface rather than *ws.Hub so the handlers can be exercised
// without a running hub, and so the write path stays honest about its
// dependency: it publishes, it does not own the registry.
type Publisher interface {
	Publish(shopID string, eventType ws.EventType, payload any) bool
}

// maxBodyBytes caps how much a write request may send. The capture payload is
// a few hundred bytes; anything approaching this bound is a mistake or an
// attack, and refusing it early keeps the decoder from buffering it whole.
const maxBodyBytes = 1 << 20 // 1 MiB

// Server holds the dependencies shared by the handlers.
type Server struct {
	store          *storage.Store
	publisher      Publisher
	resolveShop    auth.ShopResolver
	log            *slog.Logger
	validateEngine *validate.Engine
}

// New builds the /api/ handler tree, to be mounted at the "/api/" prefix.
//
// publisher may be nil for a read-only deployment; the write handlers then
// record the change but tell nobody about it, which is the correct behaviour
// for a process that has no hub.
func New(store *storage.Store, publisher Publisher, resolveShop auth.ShopResolver, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if resolveShop == nil {
		resolveShop = auth.QueryShopResolver
	}

	server := &Server{
		store:          store,
		publisher:      publisher,
		resolveShop:    resolveShop,
		log:            logger,
		validateEngine: validate.NewEngine(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/transactions", server.handleTransactions)
	mux.HandleFunc("/api/transactions/export", server.handleExport)
	mux.HandleFunc("/api/transactions/", server.handleTransactionDetail)
	mux.HandleFunc("/api/inventory", server.handleInventory)
	mux.HandleFunc("/api/products", server.handleProducts)
	mux.HandleFunc("/api/products/template", server.handleProductsTemplate)
	mux.HandleFunc("/api/products/import", server.handleProductsImport)
	mux.HandleFunc("/api/products/", server.handleProductDetail)
	mux.HandleFunc("/api/summary", server.handleSummary)
	mux.HandleFunc("/api/settings", server.handleSettings)
	mux.HandleFunc("/api/captures", server.handleCaptures)
	mux.HandleFunc("/api/devices", server.handleDevices)
	mux.HandleFunc("/api/devices/", server.handleDeviceDetail)
	// Catch-all, so an unknown path under /api/ answers in JSON like every
	// other endpoint rather than the mux's plain-text 404.
	mux.HandleFunc("/api/", server.handleNotFound)

	var handler http.Handler = mux
	handler = Recoverer(logger)(handler)
	handler = RequestIDMiddleware(handler)
	return handler
}

// publish broadcasts an event, logging rather than failing when it is dropped.
//
// A dropped event is not a failed request: the write has already committed, and
// a dashboard that missed the notification recovers by refetching a snapshot on
// its next reconnect. Failing the request here would trade a guaranteed-lost
// notification for a client that thinks its write failed and retries it.
func (s *Server) publish(w http.ResponseWriter, r *http.Request, shopID string, eventType ws.EventType, payload any) {
	if s.publisher == nil {
		return
	}
	if !s.publisher.Publish(shopID, eventType, payload) {
		s.log.Warn("api: event not delivered",
			"type", eventType, "shop_id", shopID, "path", r.URL.Path)
	}
}

// decodeJSON reads a request body into target, writing the 400 itself when the
// body is unusable.
//
// Unknown fields are rejected so that a client sending a misspelled anchor
// ("quantitiy") is told, rather than having the field silently default.
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// newID mints a prefixed identifier.
//
// Identifiers are generated here rather than by the database so the same value
// can be used in the response, in the event published to dashboards, and in
// logs without a read-back. The prefix keeps a stray ID recognisable in a log
// line.
func newID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("api: generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

type transactionsResponse struct {
	ShopID       string               `json:"shop_id"`
	Transactions []models.Transaction `json:"transactions"`
	Count        int                  `json:"count"`
	Total        int                  `json:"total"`
	Limit        int                  `json:"limit"`
	Offset       int                  `json:"offset"`
	// GeneratedAt is the instant the snapshot was taken.
	//
	// A client resyncing after a stream gap uses it to discard events it
	// receives that are older than the snapshot: those are already reflected
	// here, and applying them again would double-count. Without it, a
	// reconnect can only choose between losing events and duplicating them.
	GeneratedAt time.Time `json:"generated_at"`
}

type inventoryResponse struct {
	ShopID   string           `json:"shop_id"`
	Products []models.Product `json:"products"`
	Count    int              `json:"count"`
	// LowStockCount is computed here rather than by the client so the
	// reorder rule lives in exactly one place (models.Product.IsLowStock).
	LowStockCount int       `json:"low_stock_count"`
	GeneratedAt   time.Time `json:"generated_at"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "unknown endpoint")
}

// shopID resolves the requesting shop, writing the error response itself when
// it cannot.
//
// The 401 is deliberate even though the parameter it currently checks is
// missing rather than invalid: the status describes a failure to establish
// identity, which is what this will mean once the resolver reads a session.
func (s *Server) shopID(w http.ResponseWriter, r *http.Request) (string, bool) {
	shopID, err := s.resolveShop(r)
	if err != nil {
		// The resolver's reason is not echoed: it could reveal which shops
		// exist.
		s.log.Warn("api: rejected request", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusUnauthorized, "a valid shop_id is required")
		return "", false
	}
	return shopID, true
}

// limitParam reads ?limit=, clamped to the range the store supports.
func (s *Server) limitParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return storage.DefaultTransactionLimit, true
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		writeError(w, http.StatusBadRequest, "limit must be a positive integer")
		return 0, false
	}
	return storage.ClampLimit(limit), true
}

// fail logs the underlying error and returns a generic message: storage errors
// name tables and file paths, which are not the client's business.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, operation string, err error) {
	s.log.Error("api: "+operation, "error", err, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// The response headers are already committed, so an encode failure can
	// only be logged — the client will see a truncated body.
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func methodNotAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
