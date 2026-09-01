package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/app"
	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
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
