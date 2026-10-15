package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/eeyung1/listenledger-backend/storage"
)

const CookieName = "listenledger_session"
const sessionLife = 12 * time.Hour
const passwordIterations = 600000

type Service struct {
	Store         *storage.Store
	SecureCookies bool
}

func randomToken() string { return hex.EncodeToString(randomBytes(32)) }
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func passwordHash(password string) string {
	salt := randomBytes(16)
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(key)
}
func passwordMatches(encoded, password string) bool {
	parts := strings.Split(encoded, ":")
	if len(parts) != 2 {
		return false
	}
	salt, e := hex.DecodeString(parts[0])
	if e != nil || len(salt) != 16 {
		return false
	}
	expected, e := hex.DecodeString(parts[1])
	if e != nil || len(expected) != 32 {
		return false
	}
	key, e := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	return e == nil && subtle.ConstantTimeCompare(key, expected) == 1
}
func (s *Service) Identity(r *http.Request) (storage.Identity, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return storage.Identity{}, err
	}
	return s.Store.SessionIdentity(r.Context(), digest(cookie.Value))
}
func (s *Service) ResolveShop(r *http.Request) (string, error) {
	a, err := s.Identity(r)
	if err != nil {
		return "", errors.New("authentication required")
	}
	if claimed := r.URL.Query().Get("shop_id"); claimed != "" && claimed != a.ShopID {
		return "", errors.New("shop access denied")
	}
	return a.ShopID, nil
}

// Browser writes require a non-simple custom header and reject cross-origin requests.
// CORS is not enabled; an attacker cannot supply this header from another origin.
func ProtectWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" {
			// Machine-to-machine requests authenticated by an edge device Bearer token
			// are not vulnerable to browser cross-origin request forgery.
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("X-ListenLedger-Request") != "1" {
				reply(w, 403, map[string]string{"error": "request verification required"})
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host != r.Host || u.User != nil {
					reply(w, 403, map[string]string{"error": "origin denied"})
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				reply(w, 403, map[string]string{"error": "origin denied"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Service) Handler() http.Handler { return ProtectWrites(http.HandlerFunc(s.serve)) }

type request struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	ShopName     string `json:"shop_name"`
	RecoveryCode string `json:"recovery_code"`
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/auth/session" && r.Method == "GET" {
		a, e := s.Identity(r)
		if e != nil {
			reply(w, 401, map[string]string{"error": "sign in required"})
			return
		}
		reply(w, 200, a)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		reply(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	if r.URL.Path == "/api/auth/logout" {
		c, e := r.Cookie(CookieName)
		if e == nil {
			if err := s.Store.DeleteSession(r.Context(), digest(c.Value)); err != nil {
				reply(w, 500, map[string]string{"error": "sign out failed"})
				return
			}
		}
		s.cookie(w, "", -1)
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path != "/api/auth/signup" && r.URL.Path != "/api/auth/login" && r.URL.Path != "/api/auth/recover" {
		reply(w, 404, map[string]string{"error": "unknown endpoint"})
		return
	}
	var b request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&b) != nil || dec.Decode(&struct{}{}) != io.EOF {
		reply(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	b.Email = strings.ToLower(strings.TrimSpace(b.Email))
	addr, err := mail.ParseAddress(b.Email)
	if err != nil || addr.Address != b.Email || len(b.Email) > 254 || len(b.Password) > 128 || len(b.Password) < 12 {
		reply(w, 400, map[string]string{"error": "use a valid email and a password of 12–128 bytes"})
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	for key, limit := range map[string]int{"ip:" + digest(ip): 60, "account:" + digest(b.Email): 10} {
		ok, err := s.Store.AllowAuthAttempt(r.Context(), key, limit)
		if err != nil {
			reply(w, 503, map[string]string{"error": "authentication unavailable"})
			return
		}
		if !ok {
			w.Header().Set("Retry-After", "900")
			reply(w, 429, map[string]string{"error": "too many attempts; try again in 15 minutes"})
			return
		}
	}
	var a storage.Account
	var recovery string
	switch r.URL.Path {
	case "/api/auth/signup":
		b.ShopName = strings.TrimSpace(b.ShopName)
		if b.ShopName == "" || len(b.ShopName) > 120 {
			reply(w, 400, map[string]string{"error": "shop name required (maximum 120 bytes)"})
			return
		}
		recovery = randomToken()
		a = storage.Account{Identity: storage.Identity{OwnerID: "owner_" + randomToken(), Email: b.Email, ShopID: "shop_" + randomToken(), ShopName: b.ShopName, Currency: "NGN", Timezone: "Africa/Lagos"}, PasswordHash: passwordHash(b.Password), RecoveryHash: digest(recovery)}
		if s.Store.CreateAccount(r.Context(), a) != nil {
			reply(w, 409, map[string]string{"error": "account could not be created; try signing in or recovery"})
			return
		}
	case "/api/auth/login":
		a, err = s.Store.AccountByEmail(r.Context(), b.Email)
		// Use an equivalent-cost hash for unknown accounts to avoid a cheap enumeration path.
		hash := a.PasswordHash
		if err != nil {
			hash = strings.Repeat("00", 16) + ":" + strings.Repeat("00", 32)
		}
		if !passwordMatches(hash, b.Password) || err != nil {
			reply(w, 401, map[string]string{"error": "invalid credentials"})
			return
		}
	case "/api/auth/recover":
		a, err = s.Store.AccountByEmail(r.Context(), b.Email)
		if err != nil || b.RecoveryCode == "" || subtle.ConstantTimeCompare([]byte(a.RecoveryHash), []byte(digest(b.RecoveryCode))) != 1 {
			reply(w, 401, map[string]string{"error": "invalid recovery details"})
			return
		}
		recovery = randomToken()
		a.PasswordHash = passwordHash(b.Password)
		if s.Store.RecoverAccount(r.Context(), a.OwnerID, a.RecoveryHash, a.PasswordHash, digest(recovery)) != nil {
			reply(w, 409, map[string]string{"error": "recovery failed; request a fresh attempt"})
			return
		}
	}
	token := randomToken()
	if s.Store.CreateSession(r.Context(), digest(token), a.OwnerID, a.PasswordHash, time.Now().Add(sessionLife)) != nil {
		reply(w, 500, map[string]string{"error": "session could not be created; sign in again"})
		return
	}
	s.cookie(w, token, int(sessionLife.Seconds()))
	reply(w, 200, struct {
		storage.Identity
		RecoveryCode string `json:"recovery_code,omitempty"`
	}{a.Identity, recovery})
}
func (s *Service) cookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}
