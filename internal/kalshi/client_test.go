package kalshi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
)

func TestTranslateFill(t *testing.T) {
	message := wsMessage{Type: "fill", Msg: json.RawMessage(`{"trade_id":"trade-1","order_id":"order-1","market_ticker":"TEST","is_taker":true,"side":"yes","yes_price_dollars":"0.5000","count_fp":"100.00","action":"buy","ts_ms":1700000000000}`)}
	event, ok, err := translate(message)
	if err != nil || !ok {
		t.Fatalf("translate: ok=%v err=%v", ok, err)
	}
	fill, ok := event.Data.(domain.Fill)
	if !ok {
		t.Fatalf("unexpected fill type %T", event.Data)
	}
	if fill.ID != "trade-1" || fill.OrderID != "order-1" || fill.Quantity != 100*domain.Dollar || fill.RawPrice != 5000 || fill.Fee != 17500 {
		t.Fatalf("unexpected fill %+v", fill)
	}
}

func TestTranslateOrderBook(t *testing.T) {
	message := wsMessage{Type: "orderbook_snapshot", Seq: 22, Msg: json.RawMessage(`{"market_ticker":"TEST","yes_dollars_fp":[["0.5000","100.00"]],"no_dollars_fp":[["0.4900","25.00"]]}`)}
	event, ok, err := translate(message)
	if err != nil || !ok {
		t.Fatalf("translate: ok=%v err=%v", ok, err)
	}
	book := event.Data.(domain.OrderBook)
	if book.Sequence != 22 || book.Yes[0].Price != 5000 || book.Yes[0].Quantity != 100*domain.Dollar {
		t.Fatalf("unexpected book %+v", book)
	}
}

func TestNormalizeOrderUsesCurrentFixedPointFields(t *testing.T) {
	created := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	order := normalizeOrder(rawOrder{ID: "order-1", Ticker: "TEST", Status: "resting", Side: "no", NoPrice: "0.4400", Filled: "2.50", Remaining: "7.50", Initial: "10.00", Created: created})
	if order.ID != "order-1" || order.LimitPrice != 4400 || order.Quantity != 10*domain.Dollar || order.FilledQuantity != 25_000 {
		t.Fatalf("unexpected normalized order %+v", order)
	}
	if order.CashRisk <= 0 || !order.CreatedAt.Equal(created) {
		t.Fatalf("expected risk and timestamp, got %+v", order)
	}
}

func TestPlaceNoOrderUsesV2AskOnYesBook(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/portfolio/events/orders" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["side"] != "ask" || body["price"] != "0.4400" || body["count"] != "10.0000" || body["time_in_force"] != "good_till_canceled" {
			t.Fatalf("unexpected V2 body %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"order":{"order_id":"order-1","ticker":"TEST","status":"resting","side":"no","no_price_dollars":"0.5600","initial_count_fp":"10.0000","remaining_count_fp":"10.0000"}}`))
	}))
	defer server.Close()
	client := &Client{cfg: Config{Environment: "demo", KeyID: "key-id"}, baseURL: server.URL, key: key, http: server.Client()}
	order, err := client.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{Ticker: "TEST", OutcomeSide: "no", Quantity: 10 * domain.Dollar, LimitPrice: 5600, TimeInForce: "good_till_canceled", ClientOrderID: "client-1"})
	if err != nil {
		t.Fatal(err)
	}
	if order.ID != "order-1" || order.Side != "no" || order.LimitPrice != 5600 {
		t.Fatalf("unexpected order %+v", order)
	}
}

func TestAmendNoOrderUsesV2TotalCountAndYesBookAsk(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/portfolio/events/orders/order-1/amend" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["side"] != "ask" || body["price"] != "0.4400" || body["count"] != "10.0000" || body["client_order_id"] != "client-1" || body["updated_client_order_id"] != "client-2" || body["exchange_index"] != float64(-1) {
			t.Fatalf("unexpected amend V2 body %#v", body)
		}
		_, _ = w.Write([]byte(`{"order_id":"order-1","client_order_id":"client-2","remaining_count":"8.0000","fill_count":"0.0000","ts_ms":1788206400000}`))
	}))
	defer server.Close()
	client := &Client{cfg: Config{Environment: "demo", KeyID: "key-id"}, baseURL: server.URL, key: key, http: server.Client()}
	order, err := client.AmendOrder(context.Background(), exchange.AmendOrderRequest{OrderID: "order-1", Ticker: "TEST", OutcomeSide: "no", Quantity: 10 * domain.Dollar, LimitPrice: 5600, ClientOrderID: "client-1", UpdatedClientOrderID: "client-2"})
	if err != nil {
		t.Fatal(err)
	}
	if order.ID != "order-1" || order.Side != "no" || order.LimitPrice != 5600 || order.Quantity != 10*domain.Dollar || order.Status != "resting" {
		t.Fatalf("unexpected amended order %+v", order)
	}
}

func TestProductionClientRefusesOrderMutation(t *testing.T) {
	client := &Client{cfg: Config{Environment: "production"}}
	if _, err := client.PlaceOrder(context.Background(), exchange.PlaceOrderRequest{Ticker: "TEST", OutcomeSide: "yes", Quantity: domain.Dollar, LimitPrice: 5000}); err == nil {
		t.Fatal("production order placement was not locked")
	}
	if err := client.CancelOrder(context.Background(), "order-1"); err == nil {
		t.Fatal("production cancellation was not locked")
	}
	if _, err := client.AmendOrder(context.Background(), exchange.AmendOrderRequest{OrderID: "order-1", Ticker: "TEST", OutcomeSide: "yes", Quantity: domain.Dollar, LimitPrice: 5000}); err == nil {
		t.Fatal("production amendment was not locked")
	}
}

func TestFillsUsesOrderScopedCurrentFixedPointFields(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/portfolio/fills" || r.URL.Query().Get("order_id") != "child-1" || r.URL.Query().Get("limit") != "1000" {
			t.Fatalf("unexpected request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"fills":[{"fill_id":"fill-1","order_id":"child-1","market_ticker":"TEST","outcome_side":"no","no_price_dollars":"0.4000","count_fp":"2.0000","fee_cost":"0.1000","created_time":"2026-08-31T20:00:00Z"}],"cursor":""}`))
	}))
	defer server.Close()
	client := &Client{cfg: Config{Environment: "production", KeyID: "key-id"}, baseURL: server.URL, key: key, http: server.Client()}
	fills, err := client.Fills(context.Background(), []string{"child-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fills) != 1 || fills[0].ID != "fill-1" || fills[0].OrderID != "child-1" || fills[0].Side != "no" || fills[0].RawPrice != 4000 || fills[0].Fee != 1000 || fills[0].CashRisk != 9000 {
		t.Fatalf("unexpected fills %+v", fills)
	}
}

func TestBalanceConvertsCentsToInternalMoney(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/portfolio/balance" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"balance":123456,"portfolio_value":130000}`))
	}))
	defer server.Close()
	client := &Client{cfg: Config{Environment: "production", KeyID: "key-id"}, baseURL: server.URL, key: key, http: server.Client()}
	balance, err := client.Balance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if balance != 12_345_600 {
		t.Fatalf("balance got %d want 12345600", balance)
	}
}
