package ws

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/eeyung1/listenledger-backend/auth"
)

// Handler upgrades HTTP requests to WebSocket connections and hands them to
// the hub.
type Handler struct {
	hub         *Hub
	resolveShop auth.ShopResolver
	upgrader    websocket.Upgrader
	log         *slog.Logger
}

// NewHandler builds the /ws endpoint.
//
// allowedOrigins is the list of browser Origins permitted to open a socket.
// An empty list means same-origin only, which is correct whenever the
// dashboard is served by this same server.
func NewHandler(hub *Hub, resolveShop auth.ShopResolver, allowedOrigins []string, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if resolveShop == nil {
		resolveShop = auth.QueryShopResolver
	}

	return &Handler{
		hub:         hub,
		resolveShop: resolveShop,
		log:         logger,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   1024,
			WriteBufferSize:  4096,
			CheckOrigin:      originChecker(allowedOrigins),
		},
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	shopID, err := h.resolveShop(r)
	if err != nil {
		h.log.Warn("ws: rejected connection", "remote", r.RemoteAddr, "error", err)
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written its own error response.
		h.log.Warn("ws: upgrade failed", "shop_id", shopID, "error", err)
		return
	}

	client := h.hub.newClient(conn, shopID)
	client.authorized = func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		current, err := h.resolveShop(r.Clone(ctx))
		return err == nil && current == shopID
	}
	go client.WritePump()
	go client.ReadPump()
}

// originChecker builds the cross-origin policy for the upgrader.
//
// A nil CheckOrigin would make gorilla apply its own default, but stating the
// rule here keeps the policy reviewable: browsers enforce Origin, so it is a
// CSRF-style guard for the browser case, not an authentication mechanism.
func originChecker(allowed []string) func(*http.Request) bool {
	if len(allowed) == 0 {
		return func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// No Origin header means a non-browser client — a native edge
				// device or a test harness. Absence is not evidence of a
				// cross-site request; authentication gates those clients.
				return true
			}
			parsed, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return strings.EqualFold(parsed.Host, r.Host)
		}
	}

	permitted := make(map[string]struct{}, len(allowed))
	for _, origin := range allowed {
		permitted[strings.TrimSpace(origin)] = struct{}{}
	}

	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		_, ok := permitted[origin]
		return ok
	}
}
