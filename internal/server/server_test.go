package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/app"
	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/storage"
)

func TestProductionHealthDeclaresHardTradingLock(t *testing.T) {
	service := app.New(app.Config{ExchangeEnvironment: "production"}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()
	New(service, nil).Handler().ServeHTTP(response, request)
	var health domain.Health
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || health.TradingEnabled || health.TradingLock != "Production order entry is hard-locked." {
		t.Fatalf("unsafe production health response: status=%d health=%+v", response.Code, health)
	}
}

func TestAuditEndpointIsBoundedAndCursorBased(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, kind := range []string{"one", "two", "three"} {
		if err := store.Audit(context.Background(), kind, map[string]string{"kind": kind}); err != nil {
			t.Fatal(err)
		}
	}
	service := app.New(app.Config{ExchangeEnvironment: "production"}, store, nil)
	handler := New(service, nil).Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/audit?limit=2", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var first app.AuditPage
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(first.Records) != 2 || first.Records[0].Kind != "three" || !first.HasMore || first.NextBefore == 0 {
		t.Fatalf("unexpected first audit response: status=%d page=%+v", response.Code, first)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/audit?limit=2&before="+strconv.FormatInt(first.NextBefore, 10), nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var second app.AuditPage
	if err := json.NewDecoder(response.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if len(second.Records) != 1 || second.Records[0].Kind != "one" || second.HasMore {
		t.Fatalf("unexpected second audit response: %+v", second)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/audit?before=-1", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMappingReviewEndpointsValidateCandidateAndPersistGroup(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "mapping-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	review := domain.MappingReview{ID: "group-1", Exchange: "kalshi", Title: "UMass vs Rutgers", Tickers: []string{"A", "B"}, Candidates: []domain.MappingCandidate{{EventID: "141", Score: 100}}}
	if err := store.ReplaceMappingReviews(ctx, "kalshi", []domain.MappingReview{review}); err != nil {
		t.Fatal(err)
	}
	handler := New(app.New(app.Config{ExchangeEnvironment: "production"}, store, nil), nil).Handler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/mapping-reviews?limit=10", nil))
	var reviews []domain.MappingReview
	if err := json.NewDecoder(response.Body).Decode(&reviews); err != nil || response.Code != http.StatusOK || len(reviews) != 1 {
		t.Fatalf("unexpected review list: status=%d reviews=%+v err=%v", response.Code, reviews, err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/mapping-reviews/group-1", bytes.NewBufferString(`{"eventId":"unrelated"}`)))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid candidate status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/mapping-reviews/group-1", bytes.NewBufferString(`{"eventId":"141"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("valid candidate status=%d body=%s", response.Code, response.Body.String())
	}
	overrides, err := store.LoadMappingOverrides(ctx, "kalshi")
	if err != nil || len(overrides) != 2 || overrides["A"].EventID != "141" || overrides["B"].Status != "manual_accepted" {
		t.Fatalf("unexpected group overrides %+v err=%v", overrides, err)
	}
}

func TestParentOrderEndpointIsLockedByDefault(t *testing.T) {
	service := app.New(app.Config{ExchangeEnvironment: "production"}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/parent-orders", bytes.NewBufferString(`{"eventId":"1","ticker":"TEST","outcome":"Yes","market":"moneyline","side":"yes","strategy":"basic","policy":"limit","cashRisk":10000,"priceCapMoneyline":-107,"limitPrice":5000}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	New(service, nil).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestBulkCancelEndpointIsLockedInProduction(t *testing.T) {
	service := app.New(app.Config{ExchangeEnvironment: "production"}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/parent-orders/cancel", bytes.NewBufferString(`{"scope":"all"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	New(service, nil).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestBulkCancelEndpointRejectsUnknownScope(t *testing.T) {
	service := app.New(app.Config{ExchangeEnvironment: "demo", DemoTrading: true}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/parent-orders/cancel", bytes.NewBufferString(`{"scope":"everything"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	New(service, nil).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestResumeEndpointIsLockedInProduction(t *testing.T) {
	service := app.New(app.Config{ExchangeEnvironment: "production"}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/parent-orders/parent-1/resume", nil)
	response := httptest.NewRecorder()
	New(service, nil).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestIndividualCancelEndpointIsLockedInProduction(t *testing.T) {
	service := app.New(app.Config{ExchangeEnvironment: "production"}, nil, nil)
	request := httptest.NewRequest(http.MethodDelete, "/api/parent-orders/parent-1", nil)
	response := httptest.NewRecorder()
	New(service, nil).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestSecurityHeadersAndCrossOriginWebSocketRejection(t *testing.T) {
	service := app.New(app.Config{ExchangeEnvironment: "production"}, nil, nil)
	handler := New(service, nil).Handler()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("security headers missing: %+v", response.Header())
	}
	request := httptest.NewRequest(http.MethodGet, "http://station.local/api/ws", nil)
	request.Header.Set("Origin", "https://attacker.example")
	if sameOrigin(request) {
		t.Fatal("cross-origin websocket request was accepted")
	}
}
