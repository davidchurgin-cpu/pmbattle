package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/app"
	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	orderengine "github.com/davidchurgin-cpu/pmbattle/internal/orders"
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
	mux.HandleFunc("POST /api/account/refresh", s.refreshAccount)
	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("GET /api/audit", s.audit)
	mux.HandleFunc("GET /api/mapping-reviews", s.mappingReviews)
	mux.HandleFunc("POST /api/mapping-reviews/{id}", s.decideMappingReview)
	mux.HandleFunc("PUT /api/settings", s.updateSettings)
	mux.HandleFunc("GET /api/books/{ticker}", s.book)
	mux.HandleFunc("DELETE /api/books/{ticker}", s.releaseBook)
	mux.HandleFunc("POST /api/parent-orders", s.createParentOrder)
	mux.HandleFunc("DELETE /api/orders/{id}", s.cancelOrder)
	mux.HandleFunc("PATCH /api/orders/{id}", s.amendOrder)
	mux.HandleFunc("DELETE /api/orders", s.cancelAllOrders)
	mux.HandleFunc("POST /api/parent-orders/cancel", s.cancelParentOrders)
	mux.HandleFunc("POST /api/parent-orders/{id}/resume", s.resumeParentOrder)
	mux.HandleFunc("DELETE /api/parent-orders/{id}", s.cancelParentOrder)
	mux.HandleFunc("GET /api/ws", s.ws)
	if s.static != nil {
		mux.Handle("/", spaHandler(s.static))
	}
	return securityHeaders(guard(mux))
}
func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	order, err := s.service.CancelOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, orderengine.ErrDisabled) {
			status = http.StatusForbidden
		} else if errors.Is(err, app.ErrOrderNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, order)
}
func (s *Server) amendOrder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var input struct {
		RemainingQuantity domain.Money `json:"remainingQuantity"`
		LimitPrice        domain.Money `json:"limitPrice"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid order edit"})
		return
	}
	order, err := s.service.AmendOrder(r.Context(), r.PathValue("id"), input.RemainingQuantity, input.LimitPrice)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, orderengine.ErrDisabled):
			status = http.StatusForbidden
		case errors.Is(err, app.ErrOrderNotFound):
			status = http.StatusNotFound
		case errors.Is(err, app.ErrOrderNotEditable), errors.Is(err, orderengine.ErrInvalidOrder), errors.Is(err, orderengine.ErrCashRiskCap):
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, order)
}
func (s *Server) cancelAllOrders(w http.ResponseWriter, r *http.Request) {
	result, err := s.service.CancelAllOrders(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, orderengine.ErrDisabled) {
			status = http.StatusForbidden
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusOK
	if len(result.Failures) > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.Snapshot().Health)
}
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.Snapshot())
}
func (s *Server) refreshAccount(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.RefreshAccount(r.Context()))
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.service.Snapshot().Settings)
}
func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	before, err := parseNonnegativeInt64(r.URL.Query().Get("before"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid audit cursor"})
		return
	}
	limit, err := parseNonnegativeInt64(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid audit limit"})
		return
	}
	page, err := s.service.AuditHistory(r.Context(), before, int(limit))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load audit history"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) mappingReviews(w http.ResponseWriter, r *http.Request) {
	limit, err := parseNonnegativeInt64(r.URL.Query().Get("limit"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid mapping review limit"})
		return
	}
	reviews, err := s.service.MappingReviews(r.Context(), int(limit))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load mapping reviews"})
		return
	}
	writeJSON(w, http.StatusOK, reviews)
}

func (s *Server) decideMappingReview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input app.MappingDecisionInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid mapping decision"})
		return
	}
	review, err := s.service.DecideMappingReview(r.Context(), r.PathValue("id"), input)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, app.ErrMappingReviewNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, app.ErrInvalidMappingDecision) {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func parseNonnegativeInt64(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("value must be a nonnegative integer")
	}
	return parsed, nil
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
func (s *Server) createParentOrder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input struct {
		EventID           string       `json:"eventId"`
		Ticker            string       `json:"ticker"`
		Rotation          string       `json:"rotation"`
		Outcome           string       `json:"outcome"`
		Market            string       `json:"market"`
		Side              string       `json:"side"`
		Strategy          string       `json:"strategy"`
		Policy            string       `json:"policy"`
		CashRisk          domain.Money `json:"cashRisk"`
		PriceCapMoneyline int64        `json:"priceCapMoneyline"`
		LimitPrice        domain.Money `json:"limitPrice"`
		SliceQuantity     domain.Money `json:"sliceQuantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid order request"})
		return
	}
	parent, err := s.service.CreateParentOrder(r.Context(), app.CreateParentOrderInput{
		EventID: input.EventID, Ticker: input.Ticker, Rotation: input.Rotation, Outcome: input.Outcome,
		Market: input.Market, Side: input.Side, Strategy: input.Strategy, Policy: input.Policy,
		CashRisk: input.CashRisk, PriceCapMoneyline: input.PriceCapMoneyline, LimitPrice: input.LimitPrice,
		SliceQuantity: input.SliceQuantity,
	})
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, orderengine.ErrDisabled) {
			status = http.StatusForbidden
		} else if strings.Contains(err.Error(), "synchronized") {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, parent)
}
func (s *Server) cancelParentOrder(w http.ResponseWriter, r *http.Request) {
	parent, err := s.service.CancelParentOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, orderengine.ErrDisabled) {
			status = http.StatusForbidden
		} else if errors.Is(err, orderengine.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, parent)
}
func (s *Server) resumeParentOrder(w http.ResponseWriter, r *http.Request) {
	parent, err := s.service.ResumeParentOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, orderengine.ErrDisabled) {
			status = http.StatusForbidden
		} else if errors.Is(err, orderengine.ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, orderengine.ErrNotResumable) || strings.Contains(err.Error(), "synchronized") {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error(), "parent": parent})
		return
	}
	writeJSON(w, http.StatusOK, parent)
}
func (s *Server) cancelParentOrders(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var input app.CancelScopeInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cancel request"})
		return
	}
	result, err := s.service.CancelParentOrders(r.Context(), input)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, orderengine.ErrDisabled) {
			status = http.StatusForbidden
		} else if !errors.Is(err, app.ErrInvalidCancelScope) {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusOK
	if len(result.Failures) > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
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
		w.Header().Set("X-Frame-Options", "DENY")
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
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}
