package app

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
	"github.com/davidchurgin-cpu/pmbattle/internal/storage"
)

type appFakeAdapter struct{ placed []exchange.PlaceOrderRequest }

func (f *appFakeAdapter) Name() string { return "fake" }
func (f *appFakeAdapter) ListMarkets(context.Context, []domain.CanonicalEvent) ([]domain.CanonicalMarket, error) {
	return nil, nil
}
func (f *appFakeAdapter) SubscribeAccount(context.Context) (*exchange.Subscription, error) {
	return nil, nil
}
func (f *appFakeAdapter) SubscribeBooks(context.Context, []string) (*exchange.Subscription, error) {
	return nil, nil
}
func (f *appFakeAdapter) Snapshot(context.Context) ([]domain.Order, []domain.Position, []domain.Fill, error) {
	return nil, nil, nil, nil
}
func (f *appFakeAdapter) Balance(context.Context) (domain.Money, error) { return 0, nil }
func (f *appFakeAdapter) Fills(context.Context, []string) ([]domain.Fill, error) {
	return nil, nil
}
func (f *appFakeAdapter) PlaceOrder(_ context.Context, request exchange.PlaceOrderRequest) (domain.Order, error) {
	f.placed = append(f.placed, request)
	return domain.Order{ID: fmt.Sprintf("child-%d", len(f.placed)), Exchange: "Kalshi", Ticker: request.Ticker, Side: request.OutcomeSide, Status: "resting", Quantity: request.Quantity, LimitPrice: request.LimitPrice}, nil
}
func (f *appFakeAdapter) CancelOrder(context.Context, string) error { return nil }

func TestFillReconcilesAndPersistsParentBeforeBrowserEvent(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "reconcile.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(Config{}, store, nil)
	parent := domain.ParentOrder{ID: "parent-1", EventID: "event-1", Ticker: "TEST", Rotation: "451", Outcome: "Team A", Market: "moneyline", Side: "yes", Status: "resting", CashRiskTarget: 100 * domain.Dollar, ReservedRisk: 90 * domain.Dollar, RemainingRisk: 100 * domain.Dollar, LimitPrice: 5000, Quantity: 190 * domain.Dollar, ChildOrderIDs: []string{"child-1"}}
	service.orderEngine.Restore([]domain.ParentOrder{parent})
	service.snapshot.ParentOrders = []domain.ParentOrder{parent}
	events, cancel := service.Subscribe()
	defer cancel()
	fill := domain.Fill{ID: "fill-1", OrderID: "child-1", Exchange: "Kalshi", Ticker: "TEST", Quantity: 10 * domain.Dollar, CashRisk: 5 * domain.Dollar}
	service.handleExchangeEvent(domain.StreamEvent{Type: "fill", Data: fill})
	first, second := <-events, <-events
	if first.Type != "parent_order" || second.Type != "fill" {
		t.Fatalf("browser event order = %q then %q", first.Type, second.Type)
	}
	snapshot := service.Snapshot()
	if len(snapshot.ParentOrders) != 1 || snapshot.ParentOrders[0].FilledRisk != fill.CashRisk || snapshot.ParentOrders[0].RemainingRisk != parent.CashRiskTarget-fill.CashRisk || snapshot.AtRisk != parent.ReservedRisk {
		t.Fatalf("risk was not reconciled before publication: %+v", snapshot.ParentOrders)
	}
	persisted, err := store.LoadParentOrders(context.Background())
	if err != nil || len(persisted) != 1 || persisted[0].ProcessedFillIDs[0] != fill.ID {
		t.Fatalf("parent was not persisted: %+v err=%v", persisted, err)
	}
	service.handleExchangeEvent(domain.StreamEvent{Type: "fill", Data: fill})
	select {
	case duplicate := <-events:
		t.Fatalf("duplicate fill was published: %+v", duplicate)
	default:
	}
}

func TestIcebergRefreshPublishesParentAndChildBeforeFill(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "iceberg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter := &appFakeAdapter{}
	service := New(Config{DemoTrading: true, ExchangeEnvironment: "demo"}, store, adapter)
	quote, err := pricing.Quote(5000, 100*domain.Dollar, false)
	if err != nil {
		t.Fatal(err)
	}
	quote.Ticker, quote.Side, quote.Outcome = "TEST", "yes", "Team A"
	service.snapshot.Events = []domain.CanonicalEvent{{ID: "event-1", Participants: []domain.Participant{{Rotation: "451", Name: "Team A"}, {Rotation: "452", Name: "Team B"}}, Markets: []domain.MarketView{{Type: domain.MarketMoneyline, Away: &quote, Status: "open"}}}}
	service.availableBooks["TEST"] = true
	service.books.Snapshot(domain.OrderBook{Ticker: "TEST", Sequence: 1})
	events, cancel := service.Subscribe()
	defer cancel()
	parent, err := service.CreateParentOrder(context.Background(), CreateParentOrderInput{EventID: "event-1", Ticker: "TEST", Side: "yes", Strategy: "iceberg", Policy: "post_only", CashRisk: 100 * domain.Dollar, PriceCapMoneyline: -107, LimitPrice: 5000, SliceQuantity: 10 * domain.Dollar})
	if err != nil {
		t.Fatal(err)
	}
	<-events
	<-events
	firstChild := parent.ChildOrderIDs[0]
	fillQuote, _ := pricing.Quote(parent.LimitPrice, parent.SliceQuantity, false)
	service.handleExchangeEvent(domain.StreamEvent{Type: "fill", Data: domain.Fill{ID: "fill-1", OrderID: firstChild, Ticker: parent.Ticker, Quantity: parent.SliceQuantity, CashRisk: fillQuote.AllInCost}})
	first, second, third := <-events, <-events, <-events
	if first.Type != "parent_order" || second.Type != "order" || third.Type != "fill" {
		t.Fatalf("iceberg browser event order = %q, %q, %q", first.Type, second.Type, third.Type)
	}
	snapshot := service.Snapshot()
	if len(adapter.placed) != 2 || len(snapshot.ParentOrders) != 1 || len(snapshot.ParentOrders[0].Children) != 2 || len(snapshot.Orders) != 2 {
		t.Fatalf("iceberg refresh was not captured: placements=%d parent=%+v orders=%+v", len(adapter.placed), snapshot.ParentOrders, snapshot.Orders)
	}
}
