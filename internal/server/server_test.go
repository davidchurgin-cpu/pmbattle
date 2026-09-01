package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/app"
)

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
