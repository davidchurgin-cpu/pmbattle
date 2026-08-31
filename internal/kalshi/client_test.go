package kalshi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
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
	if fill.ID != "trade-1" || fill.Quantity != 100*domain.Dollar || fill.RawPrice != 5000 || fill.Fee != 17500 {
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
