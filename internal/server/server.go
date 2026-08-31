package server

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/app"
	"github.com/gorilla/websocket"
)

type Server struct {
	service  *app.Service
	static   fs.FS
	upgrader websocket.Upgrader
}

func New(service *app.Service, static fs.FS) *Server {
	return &Server{service: service, static: static, upgrader: websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 4096, CheckOrigin: func(r *http.Request) bool { return sameOrigin(r) }}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/snapshot", s.snapshot)
	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("PUT /api/settings", s.updateSettings)
	mux.HandleFunc("GET /api/books/{ticker}", s.book)
	mux.HandleFunc("DELETE /api/books/{ticker}", s.releaseBook)
	mux.HandleFunc("GET /api/ws", s.ws)
	if s.static != nil {
		mux.Handle("/", spaHandler(s.static))
	}
	return securityHeaders(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.Snapshot().Health)
}
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.Snapshot())
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.Snapshot().Settings)
}
func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input struct {
		EnabledSports     []string `json:"enabledSports"`
		ExcludeAddedGames bool     `json:"excludeAddedGames"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid settings"})
		return
	}
	snapshot, err := s.service.UpdatePreferences(r.Context(), input.EnabledSports, input.ExcludeAddedGames)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to save settings"})
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
func (s *Server) book(w http.ResponseWriter, r *http.Request) {
	ticker := r.PathValue("ticker")
	if !s.service.RequestBook(ticker) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "book not available"})
		return
	}
	if book, ok := s.service.Book(ticker); ok && !book.Stale {
		writeJSON(w, http.StatusOK, book)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ticker": ticker, "sequence": 0, "stale": true, "yes": []any{}, "no": []any{}})
}
func (s *Server) releaseBook(w http.ResponseWriter, r *http.Request) {
	s.service.ReleaseBook(r.PathValue("ticker"))
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	events, cancel := s.service.Subscribe()
	defer cancel()
	_ = conn.SetReadDeadline(time.Now().Add(24 * time.Hour))
	for event := range events {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteJSON(event); err != nil {
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("write response", "error", err)
	}
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		next.ServeHTTP(w, r)
	})
}
func spaHandler(static fs.FS) http.Handler {
	files := http.FileServer(http.FS(static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			serveIndex(w, static)
			return
		}
		if _, err := fs.Stat(static, path); err != nil {
			serveIndex(w, static)
			return
		}
		r.URL.Path = "/" + path
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, static fs.FS) {
	index, err := fs.ReadFile(static, "index.html")
	if err != nil {
		http.Error(w, "PMBattle frontend is not built", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}
