package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// requestHeader must accompany every state-changing API call. Browsers cannot
// attach a custom header to a cross-site form post or a simple cross-origin
// fetch without a CORS preflight, which this server never grants, so the
// header alone blocks cross-site request forgery even when no password is set.
const requestHeader = "X-Requested-With"
const requestHeaderValue = "PMBattle"

const sessionCookie = "pmbattle_session"
const sessionLifetime = 12 * time.Hour
const maxLoginFailures = 5
const loginFailureWindow = 15 * time.Minute

type auth struct {
	mu       sync.Mutex
	digest   []byte // sha256 of the configured password; nil when no password is set
	sessions map[string]time.Time
	failures map[string][]time.Time
	now      func() time.Time
}

func newAuth(password string) *auth {
	a := &auth{sessions: make(map[string]time.Time), failures: make(map[string][]time.Time), now: func() time.Time { return time.Now().UTC() }}
	if password != "" {
		sum := sha256.Sum256([]byte(password))
		a.digest = sum[:]
	}
	return a
}

func (a *auth) required() bool { return a.digest != nil }

func (a *auth) check(password string) bool {
	if a.digest == nil {
		return false
	}
	sum := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(sum[:], a.digest) == 1
}

func (a *auth) authenticated(r *http.Request) bool {
	if !a.required() {
		return true
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.sessions[cookie.Value]
	if !ok {
		return false
	}
	if a.now().After(expires) {
		delete(a.sessions, cookie.Value)
		return false
	}
	return true
}

func (a *auth) newSession() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for existing, expires := range a.sessions {
		if now.After(expires) {
			delete(a.sessions, existing)
		}
	}
	a.sessions[token] = now.Add(sessionLifetime)
	return token, nil
}

func (a *auth) endSession(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// throttled reports whether this client has exhausted its login attempts.
func (a *auth) throttled(client string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.recentFailuresLocked(client)) >= maxLoginFailures
}

func (a *auth) recordFailure(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[client] = append(a.recentFailuresLocked(client), a.now())
}

func (a *auth) clearFailures(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, client)
}

func (a *auth) recentFailuresLocked(client string) []time.Time {
	cutoff := a.now().Add(-loginFailureWindow)
	recent := make([]time.Time, 0, len(a.failures[client]))
	for _, at := range a.failures[client] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	a.failures[client] = recent
	return recent
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// guard enforces the request header on every mutating API call and the
// session cookie on every API call except the login and session probes.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		mutating := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if mutating && r.Header.Get(requestHeader) != requestHeaderValue {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "request is missing the PMBattle request header"})
			return
		}
		if r.URL.Path == "/api/session" || r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.auth.authenticated(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in to use PMBattle"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"loginRequired": s.auth.required(), "authenticated": s.auth.authenticated(r)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.auth.required() {
		writeJSON(w, http.StatusOK, map[string]bool{"loginRequired": false, "authenticated": true})
		return
	}
	client := clientKey(r)
	if s.auth.throttled(client) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many failed sign-in attempts; wait 15 minutes and try again"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var input struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sign-in request"})
		return
	}
	if !s.auth.check(input.Password) {
		s.auth.recordFailure(client)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong password"})
		return
	}
	token, err := s.auth.newSession()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to start a session"})
		return
	}
	s.auth.clearFailures(client)
	http.SetCookie(w, s.sessionCookie(r, token, sessionLifetime))
	writeJSON(w, http.StatusOK, map[string]bool{"loginRequired": true, "authenticated": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.auth.endSession(cookie.Value)
	}
	http.SetCookie(w, s.sessionCookie(r, "", -time.Hour))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sessionCookie(r *http.Request, value string, lifetime time.Duration) *http.Cookie {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	cookie := &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}
	if lifetime > 0 {
		cookie.MaxAge = int(lifetime.Seconds())
	} else {
		cookie.MaxAge = -1
	}
	return cookie
}
