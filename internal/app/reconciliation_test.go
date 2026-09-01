package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
	"github.com/davidchurgin-cpu/pmbattle/internal/storage"
)

type appFakeAdapter struct {
	placed            []exchange.PlaceOrderRequest
	amended           []exchange.AmendOrderRequest
	canceled          []string
	failCancel        string
	markets           []domain.CanonicalMarket
	marketCalls       int
	snapshotPositions []domain.Position
	settlements       []domain.Settlement
	balance           domain.Money
	beforeSnapshot    func()
	labels            map[string]domain.MarketLabel
	described         []string
}

func (f *appFakeAdapter) Name() string { return "fake" }
func (f *appFakeAdapter) DescribeMarket(_ context.Context, ticker string) (domain.MarketLabel, error) {
	f.described = append(f.described, ticker)
	label, ok := f.labels[ticker]
	if !ok {
		return domain.MarketLabel{}, errors.New("market not found")
	}
	return label, nil
}
func (f *appFakeAdapter) ListMarkets(context.Context, []domain.CanonicalEvent) ([]domain.CanonicalMarket, error) {
	f.marketCalls++
	return append([]domain.CanonicalMarket(nil), f.markets...), nil
}
func (f *appFakeAdapter) SubscribeAccount(context.Context) (*exchange.Subscription, error) {
	return nil, nil
}
func (f *appFakeAdapter) SubscribeBooks(context.Context, []string) (*exchange.Subscription, error) {
	return nil, nil
}
func (f *appFakeAdapter) Snapshot(ctx context.Context) ([]domain.Order, []domain.Position, []domain.Fill, error) {
	if f.beforeSnapshot != nil {
		f.beforeSnapshot()
	}
	if ctx.Err() != nil {
		return nil, nil, nil, ctx.Err()
	}
	return nil, f.snapshotPositions, nil, nil
}
func (f *appFakeAdapter) Balance(ctx context.Context) (domain.Money, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return f.balance, nil
}
func (f *appFakeAdapter) Fills(context.Context, []string) ([]domain.Fill, error) {
	return nil, nil
}
func (f *appFakeAdapter) Settlements(context.Context, time.Time) ([]domain.Settlement, error) {
	return f.settlements, nil
}
func (f *appFakeAdapter) PlaceOrder(_ context.Context, request exchange.PlaceOrderRequest) (domain.Order, error) {
	f.placed = append(f.placed, request)
	return domain.Order{ID: fmt.Sprintf("child-%d", len(f.placed)), Exchange: "Kalshi", Ticker: request.Ticker, Side: request.OutcomeSide, Status: "resting", Quantity: request.Quantity, LimitPrice: request.LimitPrice}, nil
}
func (f *appFakeAdapter) AmendOrder(_ context.Context, request exchange.AmendOrderRequest) (domain.Order, error) {
	f.amended = append(f.amended, request)
	return domain.Order{ID: request.OrderID, Exchange: "Kalshi", Ticker: request.Ticker, Side: request.OutcomeSide, Status: "resting", Quantity: request.Quantity, LimitPrice: request.LimitPrice}, nil
}
func (f *appFakeAdapter) CancelOrder(_ context.Context, id string) error {
	f.canceled = append(f.canceled, id)
	if id == f.failCancel {
		return errors.New("cancel unavailable")
	}
	return nil
}

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
	first, second, third := <-events, <-events, <-events
	if first.Type != "parent_order" || second.Type != "account_summary" || third.Type != "fill" {
		t.Fatalf("browser event order = %q, %q, %q", first.Type, second.Type, third.Type)
	}
	summary := second.Data.(domain.AccountSummary)
	if summary.AtRisk != parent.ReservedRisk {
		t.Fatalf("risk summary was not updated before fill alert: %+v", summary)
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

func TestAccountReconciliationEnrichesAndPersistsPositionsAndSettlements(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "account.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settledAt := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	adapter := &appFakeAdapter{
		snapshotPositions: []domain.Position{{Exchange: "Kalshi", Ticker: "TEST", Quantity: 2 * domain.Dollar, CashRisk: domain.Dollar}},
		settlements:       []domain.Settlement{{Exchange: "fake", Ticker: "TEST", Result: "yes", NetPnL: 40 * domain.Dollar, SettledAt: settledAt}},
	}
	service := New(Config{}, store, adapter)
	quote := domain.PriceQuote{Ticker: "TEST", Outcome: "Team A"}
	service.snapshot.Events = []domain.CanonicalEvent{{ID: "event-1", Participants: []domain.Participant{{Rotation: "451", Name: "Team A"}, {Rotation: "452", Name: "Team B"}}, Markets: []domain.MarketView{{Type: domain.MarketSpread, Line: "3.5", Away: &quote}}}}
	events, cancel := service.Subscribe()
	defer cancel()
	service.reconcileAccount(context.Background(), true)
	event := <-events
	if event.Type != "position" {
		t.Fatalf("first published event = %q", event.Type)
	}
	healthEvent := <-events
	if healthEvent.Type != "health" {
		t.Fatalf("second published event = %q", healthEvent.Type)
	}
	accountEvent := <-events
	if accountEvent.Type != "account_snapshot" {
		t.Fatalf("third published event = %q", accountEvent.Type)
	}
	snapshot := service.Snapshot()
	if len(snapshot.Positions) != 1 || snapshot.Positions[0].EventID != "event-1" || snapshot.Positions[0].Rotation != "451" || snapshot.Positions[0].Market != "Spread 3.5" {
		t.Fatalf("position not enriched: %+v", snapshot.Positions)
	}
	if len(snapshot.Settlements) != 1 || snapshot.Settlements[0].EventID != "event-1" || snapshot.Settlements[0].Rotation != "451" || snapshot.Settlements[0].Market != "Spread 3.5" {
		t.Fatalf("settlement not enriched: %+v", snapshot.Settlements)
	}
	if snapshot.AtRisk != domain.Dollar {
		t.Fatalf("open position exposure missing from cash at risk: %d", snapshot.AtRisk)
	}
	account := accountEvent.Data.(domain.AccountSnapshot)
	if len(account.Settlements) != 1 || account.Settlements[0].Ticker != "TEST" {
		t.Fatalf("settlement absent from browser account snapshot: %+v", account)
	}
	persisted, err := store.LoadSettlements(context.Background(), 10)
	if err != nil || len(persisted) != 1 || !persisted[0].SettledAt.Equal(settledAt) {
		t.Fatalf("settlement not persisted: %+v err=%v", persisted, err)
	}
}

func TestCashAtRiskUsesAccountExposureWithoutDoubleCountingManagedParents(t *testing.T) {
	service := &Service{snapshot: domain.Snapshot{
		Positions:    []domain.Position{{CashRisk: 60 * domain.Dollar}},
		Orders:       []domain.Order{{ID: "active", Status: "resting", CashRisk: 40 * domain.Dollar}, {ID: "done", Status: "filled", CashRisk: 90 * domain.Dollar}},
		ParentOrders: []domain.ParentOrder{{ID: "managed", Status: "resting", FilledRisk: 60 * domain.Dollar, ReservedRisk: 40 * domain.Dollar}},
	}}
	service.recalculateParentRiskLocked()
	if service.snapshot.AtRisk != 100*domain.Dollar {
		t.Fatalf("managed exposure was double counted: %d", service.snapshot.AtRisk)
	}
	service.snapshot.ParentOrders[0].FilledRisk = 80 * domain.Dollar
	service.snapshot.ParentOrders[0].ReservedRisk = 40 * domain.Dollar
	service.recalculateParentRiskLocked()
	if service.snapshot.AtRisk != 120*domain.Dollar {
		t.Fatalf("conservative parent fallback was not used: %d", service.snapshot.AtRisk)
	}
}

func TestParentCreationReservesOneSharedAvailableBankroll(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "bankroll.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter := &appFakeAdapter{}
	service := New(Config{TradingEnabled: true, ExchangeEnvironment: "demo"}, store, adapter)
	quote, err := pricing.Quote(5000, 100*domain.Dollar, false)
	if err != nil {
		t.Fatal(err)
	}
	quote.Ticker, quote.Side, quote.Outcome = "TEST", "yes", "Team A"
	service.snapshot.Events = []domain.CanonicalEvent{{ID: "event-1", Participants: []domain.Participant{{Rotation: "451", Name: "Team A"}, {Rotation: "452", Name: "Team B"}}, Markets: []domain.MarketView{{Type: domain.MarketMoneyline, Away: &quote, Status: "open"}}}}
	service.availableBooks["TEST"] = true
	service.books.Snapshot(domain.OrderBook{Ticker: "TEST", Sequence: 1, Yes: []domain.BookLevel{{Price: 5000, Quantity: 100 * domain.Dollar}}})
	service.snapshot.Bankroll = 50 * domain.Dollar
	input := CreateParentOrderInput{EventID: "event-1", Ticker: "TEST", Side: "yes", Strategy: "basic", Policy: "post_only", CashRisk: 100 * domain.Dollar, PriceCapMoneyline: -200, LimitPrice: 5000}
	if _, err := service.CreateParentOrder(context.Background(), input); !errors.Is(err, ErrInsufficientAvailableBalance) {
		t.Fatalf("oversized bankroll request got %v", err)
	}
	if len(adapter.placed) != 0 {
		t.Fatalf("insufficient request reached exchange: %+v", adapter.placed)
	}
	service.snapshot.Bankroll = 150 * domain.Dollar
	if _, err := service.CreateParentOrder(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	service.mu.RLock()
	remaining := service.availableParentCashLocked()
	service.mu.RUnlock()
	if remaining != 50*domain.Dollar {
		t.Fatalf("shared bankroll remaining = %d, want %d", remaining, 50*domain.Dollar)
	}
	if snapshot := service.Snapshot(); snapshot.AvailableToAllocate != remaining {
		t.Fatalf("browser snapshot capacity = %d, want %d", snapshot.AvailableToAllocate, remaining)
	}
}

func TestIcebergRefreshPublishesParentAndChildBeforeFill(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "iceberg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter := &appFakeAdapter{}
	service := New(Config{TradingEnabled: true, ExchangeEnvironment: "demo"}, store, adapter)
	service.snapshot.Bankroll = 1_000 * domain.Dollar
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
	<-events
	firstChild := parent.ChildOrderIDs[0]
	fillQuote, _ := pricing.Quote(parent.LimitPrice, parent.SliceQuantity, false)
	service.handleExchangeEvent(domain.StreamEvent{Type: "fill", Data: domain.Fill{ID: "fill-1", OrderID: firstChild, Ticker: parent.Ticker, Quantity: parent.SliceQuantity, CashRisk: fillQuote.AllInCost}})
	first, second, third, fourth := <-events, <-events, <-events, <-events
	if first.Type != "parent_order" || second.Type != "order" || third.Type != "account_summary" || fourth.Type != "fill" {
		t.Fatalf("iceberg browser event order = %q, %q, %q, %q", first.Type, second.Type, third.Type, fourth.Type)
	}
	snapshot := service.Snapshot()
	if len(adapter.placed) != 2 || len(snapshot.ParentOrders) != 1 || len(snapshot.ParentOrders[0].Children) != 2 || len(snapshot.Orders) != 2 {
		t.Fatalf("iceberg refresh was not captured: placements=%d parent=%+v orders=%+v", len(adapter.placed), snapshot.ParentOrders, snapshot.Orders)
	}
}

func TestFollowUsesServerBookAndRepricesBeforeBookPublication(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "follow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter := &appFakeAdapter{}
	service := New(Config{TradingEnabled: true, ExchangeEnvironment: "demo"}, store, adapter)
	service.snapshot.Bankroll = 1_000 * domain.Dollar
	quote, err := pricing.Quote(5000, 100*domain.Dollar, false)
	if err != nil {
		t.Fatal(err)
	}
	quote.Ticker, quote.Side, quote.Outcome = "TEST", "yes", "Team A"
	service.snapshot.Events = []domain.CanonicalEvent{{ID: "event-1", Participants: []domain.Participant{{Rotation: "451", Name: "Team A"}, {Rotation: "452", Name: "Team B"}}, Markets: []domain.MarketView{{Type: domain.MarketMoneyline, Away: &quote, Status: "open"}}}}
	service.availableBooks["TEST"] = true
	service.books.Snapshot(domain.OrderBook{Ticker: "TEST", Sequence: 1, Yes: []domain.BookLevel{{Price: 5200, Quantity: 50 * domain.Dollar}}, No: []domain.BookLevel{{Price: 5400, Quantity: 50 * domain.Dollar}}})
	events, cancel := service.Subscribe()
	defer cancel()
	parent, err := service.CreateParentOrder(context.Background(), CreateParentOrderInput{EventID: "event-1", Ticker: "TEST", Side: "yes", Strategy: "follow", Policy: "limit", CashRisk: 100 * domain.Dollar, PriceCapMoneyline: -300, LimitPrice: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if parent.LimitPrice != 5200 || parent.Policy != "post_only" || len(adapter.placed) != 1 || adapter.placed[0].LimitPrice != 5200 || !adapter.placed[0].PostOnly {
		t.Fatalf("follow trusted stale slip instead of server book: parent=%+v placed=%+v", parent, adapter.placed)
	}
	<-events
	<-events
	<-events
	service.handleExchangeEvent(domain.StreamEvent{Type: "orderbook", Data: domain.OrderBook{Ticker: "TEST", Sequence: 2, Yes: []domain.BookLevel{{Price: 5300, Quantity: 40 * domain.Dollar}}, No: []domain.BookLevel{{Price: 5500, Quantity: 40 * domain.Dollar}}}})
	first, second, third, fourth := <-events, <-events, <-events, <-events
	if first.Type != "parent_order" || second.Type != "order" || third.Type != "account_summary" || fourth.Type != "orderbook" {
		t.Fatalf("follow browser event order = %q, %q, %q, %q", first.Type, second.Type, third.Type, fourth.Type)
	}
	snapshot := service.Snapshot()
	if len(adapter.amended) != 1 || adapter.amended[0].LimitPrice != 5300 || snapshot.ParentOrders[0].ReplaceCount != 1 || snapshot.ParentOrders[0].FilledRisk+snapshot.ParentOrders[0].ReservedRisk > snapshot.ParentOrders[0].CashRiskTarget {
		t.Fatalf("follow reprice was not captured safely: amended=%+v parent=%+v", adapter.amended, snapshot.ParentOrders)
	}
	persisted, err := store.LoadParentOrders(context.Background())
	if err != nil || len(persisted) != 1 || persisted[0].ReplaceCount != 1 {
		t.Fatalf("follow parent was not persisted: %+v err=%v", persisted, err)
	}
}

func TestScopedCancelMatchesManagedParentsAndReportsPartialFailures(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "bulk-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter := &appFakeAdapter{failCancel: "child-2"}
	service := New(Config{TradingEnabled: true, ExchangeEnvironment: "demo"}, store, adapter)
	now := time.Date(2026, 8, 31, 21, 0, 0, 0, time.UTC)
	parents := []domain.ParentOrder{
		{ID: "parent-1", Exchange: "Kalshi", EventID: "event-1", Ticker: "TEST-1", Strategy: "follow", Status: "resting", CashRiskTarget: 100 * domain.Dollar, RemainingRisk: 100 * domain.Dollar, ReservedRisk: 50 * domain.Dollar, LimitPrice: 5000, Quantity: 100 * domain.Dollar, ChildOrderIDs: []string{"child-1"}, Children: []domain.ChildOrderState{{ID: "child-1", Status: "resting", Quantity: 100 * domain.Dollar}}, CreatedAt: now},
		{ID: "parent-2", Exchange: "Kalshi", EventID: "event-1", Ticker: "TEST-2", Strategy: "iceberg", Status: "resting", CashRiskTarget: 100 * domain.Dollar, RemainingRisk: 100 * domain.Dollar, ReservedRisk: 50 * domain.Dollar, LimitPrice: 5000, Quantity: 100 * domain.Dollar, ChildOrderIDs: []string{"child-2"}, Children: []domain.ChildOrderState{{ID: "child-2", Status: "resting", Quantity: 100 * domain.Dollar}}, CreatedAt: now.Add(time.Second)},
		{ID: "parent-3", Exchange: "Kalshi", EventID: "event-2", Ticker: "TEST-3", Strategy: "basic", Status: "resting", CashRiskTarget: 100 * domain.Dollar, RemainingRisk: 100 * domain.Dollar, ReservedRisk: 50 * domain.Dollar, LimitPrice: 5000, Quantity: 100 * domain.Dollar, ChildOrderIDs: []string{"child-3"}, Children: []domain.ChildOrderState{{ID: "child-3", Status: "resting", Quantity: 100 * domain.Dollar}}, CreatedAt: now.Add(2 * time.Second)},
	}
	service.orderEngine.Restore(parents)
	service.snapshot.ParentOrders = parents
	service.snapshot.Orders = []domain.Order{{ID: "child-1", Status: "resting"}, {ID: "child-2", Status: "resting"}, {ID: "child-3", Status: "resting"}}
	result, err := service.CancelParentOrders(context.Background(), CancelScopeInput{Scope: "event", Value: "event-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 2 || len(result.Canceled) != 1 || result.Canceled[0].ID != "parent-1" || len(result.Failures) != 1 || result.Failures[0].ParentID != "parent-2" {
		t.Fatalf("unexpected scoped cancel result %+v", result)
	}
	if len(adapter.canceled) != 2 || adapter.canceled[0] != "child-1" || adapter.canceled[1] != "child-2" {
		t.Fatalf("wrong exchange cancellations %v", adapter.canceled)
	}
	snapshot := service.Snapshot()
	if snapshot.ParentOrders[0].Status != "canceled" || snapshot.ParentOrders[1].Status == "canceled" || snapshot.ParentOrders[2].Status == "canceled" {
		t.Fatalf("partial result was hidden in snapshot %+v", snapshot.ParentOrders)
	}
	if _, err := service.CancelParentOrders(context.Background(), CancelScopeInput{Scope: "strategy", Value: "unknown"}); !errors.Is(err, ErrInvalidCancelScope) {
		t.Fatalf("invalid strategy got %v", err)
	}
}

func TestCancelReconciledOrderWithoutParent(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "reconciled-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter := &appFakeAdapter{}
	service := New(Config{TradingEnabled: true, ExchangeEnvironment: "production"}, store, adapter)
	service.snapshot.Orders = []domain.Order{{ID: "live-order-1", Exchange: "Kalshi", Ticker: "TEST", Status: "resting", CashRisk: 50 * domain.Dollar}}

	order, err := service.CancelOrder(context.Background(), "live-order-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(adapter.canceled) != 1 || adapter.canceled[0] != "live-order-1" || order.Status != "canceled" || order.CashRisk != 0 {
		t.Fatalf("unexpected cancellation: adapter=%v order=%+v", adapter.canceled, order)
	}
	if got := service.Snapshot().Orders[0]; got.Status != "canceled" || got.CashRisk != 0 {
		t.Fatalf("snapshot was not updated: %+v", got)
	}
}

func TestResumeFollowRefusesStaleBookBeforeEngineStateChanges(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "resume-stale.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(Config{TradingEnabled: true, ExchangeEnvironment: "demo"}, store, &appFakeAdapter{})
	parent := domain.ParentOrder{ID: "parent-1", Exchange: "Kalshi", EventID: "event-1", Ticker: "TEST", Side: "yes", Strategy: "follow", Status: "paused", CashRiskTarget: 100 * domain.Dollar, RemainingRisk: 100 * domain.Dollar, ReservedRisk: 50 * domain.Dollar, LimitPrice: 5000, Quantity: 100 * domain.Dollar, ChildOrderIDs: []string{"child-1"}, Children: []domain.ChildOrderState{{ID: "child-1", Status: "resting", Quantity: 100 * domain.Dollar}}}
	service.orderEngine.Restore([]domain.ParentOrder{parent})
	service.snapshot.ParentOrders = []domain.ParentOrder{parent}
	service.books.Snapshot(domain.OrderBook{Ticker: "TEST", Yes: []domain.BookLevel{{Price: 5100, Quantity: domain.Dollar}}})
	_, _ = service.books.MarkStale("TEST")
	if _, err := service.ResumeParentOrder(context.Background(), parent.ID); err == nil || !strings.Contains(err.Error(), "synchronized") {
		t.Fatalf("stale resume got %v", err)
	}
	current, ok := service.orderEngine.Parent(parent.ID)
	if !ok || current.Status != "paused" {
		t.Fatalf("stale resume changed engine state: %+v", current)
	}
}

func TestMarketCatalogRefreshPublishesNewListingsAndClearsWithdrawnViews(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := time.Date(2026, 9, 5, 19, 30, 0, 0, time.UTC)
	event := domain.CanonicalEvent{ID: "219", Sport: "football", League: "college football", StartTime: start, Participants: []domain.Participant{{Name: "Clemson"}, {Name: "LSU"}}}
	adapter := &appFakeAdapter{markets: []domain.CanonicalMarket{
		{Exchange: "fake", ExchangeTicker: "CLEM", Type: domain.MarketMoneyline, Title: "Clemson vs LSU", Outcome: "Clemson", OccurrenceTime: start, YesBid: 2200, YesAsk: 2300, YesAskSize: 25 * domain.Dollar},
		{Exchange: "fake", ExchangeTicker: "LSU", Type: domain.MarketMoneyline, Title: "Clemson vs LSU", Outcome: "LSU", OccurrenceTime: start, YesBid: 7700, YesAsk: 7800, YesAskSize: 20 * domain.Dollar},
	}}
	service := New(Config{}, store, adapter)
	service.allEvents = []domain.CanonicalEvent{event}
	service.snapshot.Events = []domain.CanonicalEvent{event}
	events, cancel := service.Subscribe()
	defer cancel()
	service.refreshExchangeMarkets(context.Background(), true)
	first, update := <-events, <-events
	if first.Type != "health" || update.Type != "schedule" {
		t.Fatalf("catalog refresh published %q then %q, want health then schedule", first.Type, update.Type)
	}
	snapshot := service.Snapshot()
	if adapter.marketCalls != 1 || snapshot.Health.MappedMarkets != 2 || len(snapshot.Events[0].Markets) != 1 || snapshot.Events[0].Markets[0].Away == nil || snapshot.Events[0].Markets[0].Home == nil {
		t.Fatalf("new catalog was not attached: calls=%d health=%+v event=%+v", adapter.marketCalls, snapshot.Health, snapshot.Events[0])
	}
	if len(service.allEvents[0].Markets) != 1 {
		t.Fatalf("unfiltered catalog did not receive live views: %+v", service.allEvents[0])
	}

	adapter.markets = nil
	service.refreshExchangeMarkets(context.Background(), true)
	first, update = <-events, <-events
	if first.Type != "health" || update.Type != "schedule" {
		t.Fatalf("catalog withdrawal published %q then %q, want health then schedule", first.Type, update.Type)
	}
	snapshot = service.Snapshot()
	if adapter.marketCalls != 2 || snapshot.Health.MappedMarkets != 0 || len(snapshot.Events[0].Markets) != 0 || len(service.allEvents[0].Markets) != 0 {
		t.Fatalf("withdrawn catalog remained visible: calls=%d health=%+v event=%+v all=%+v", adapter.marketCalls, snapshot.Health, snapshot.Events[0], service.allEvents[0])
	}
}

func TestInterruptedReconciliationKeepsPriorAccountStateAndStaysSilent(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "interrupted.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	adapter := &appFakeAdapter{balance: 250 * domain.Dollar, beforeSnapshot: cancel}
	service := New(Config{ExchangeEnvironment: "production"}, store, adapter)
	service.snapshot.Bankroll = 500 * domain.Dollar
	service.snapshot.Health.AccountState = "ready"
	events, unsubscribe := service.Subscribe()
	defer unsubscribe()

	// The restart cancels the context after the balance call but before the
	// positions call, exactly the window a preferences save hits.
	service.reconcileAccount(ctx, true)
	select {
	case event := <-events:
		t.Fatalf("interrupted reconcile published %q", event.Type)
	default:
	}
	snapshot := service.Snapshot()
	if snapshot.Health.AccountState != "ready" {
		t.Fatalf("account state %q, want the prior ready state", snapshot.Health.AccountState)
	}
	if snapshot.Bankroll != 250*domain.Dollar {
		t.Fatalf("bankroll %d; the successful balance read before the interruption should stand", snapshot.Bankroll)
	}

	// An already-canceled context must not even flip the state to syncing.
	service.reconcileAccount(ctx, true)
	if service.Snapshot().Health.AccountState != "ready" {
		t.Fatal("pre-canceled reconcile changed account state")
	}
	select {
	case event := <-events:
		t.Fatalf("pre-canceled reconcile published %q", event.Type)
	default:
	}

	// A fresh context completes and publishes normally.
	service.reconcileAccount(context.Background(), true)
	if got := service.Snapshot().Health.AccountState; got != "ready" {
		t.Fatalf("fresh reconcile ended in state %q", got)
	}
	sawSnapshot := false
	for len(events) > 0 {
		if (<-events).Type == "account_snapshot" {
			sawSnapshot = true
		}
	}
	if !sawSnapshot {
		t.Fatal("fresh reconcile did not publish an account snapshot")
	}
}

func TestAccountRowsAreNamedFromScheduleLabelsAndTicker(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "labels.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter := &appFakeAdapter{labels: map[string]domain.MarketLabel{
		"KXNFLGAME-26SEP06DALPHI-PHI": {Ticker: "KXNFLGAME-26SEP06DALPHI-PHI", Title: "Dallas Cowboys at Philadelphia Eagles", YesOutcome: "Philadelphia Eagles", NoOutcome: "Dallas Cowboys", Type: domain.MarketMoneyline},
	}}
	service := New(Config{ExchangeEnvironment: "production"}, store, adapter)

	// A game on the board: names come from the schedule, including rotations.
	service.snapshot.Events = []domain.CanonicalEvent{{
		ID: "301", Sport: "Football", League: "College Football",
		Participants: []domain.Participant{{Rotation: "301", Name: "Clemson"}, {Rotation: "302", Name: "LSU"}},
		Markets: []domain.MarketView{{Type: domain.MarketTotal, Line: "52.5",
			Over:  &domain.PriceQuote{Ticker: "KXNCAAFTOTAL-26SEP05CLEMLSU-53", Outcome: "Over", Side: "yes"},
			Under: &domain.PriceQuote{Ticker: "KXNCAAFTOTAL-26SEP05CLEMLSU-53", Outcome: "Under", Side: "no"}}},
	}}

	over := domain.Position{Ticker: "KXNCAAFTOTAL-26SEP05CLEMLSU-53", Side: "yes"}
	service.enrichPositionLocked(&over)
	if over.Game != "#301 Clemson at #302 LSU" || over.Outcome != "Over 52.5" || over.Market != "Total 52.5" {
		t.Fatalf("schedule naming wrong: %+v", over)
	}
	// Over and Under share one ticker, so the side must pick the outcome.
	under := domain.Position{Ticker: "KXNCAAFTOTAL-26SEP05CLEMLSU-53", Side: "no"}
	service.enrichPositionLocked(&under)
	if under.Outcome != "Under 52.5" {
		t.Fatalf("no side named %q, want Under 52.5", under.Outcome)
	}

	// A market off the board: the stored label names it.
	service.lookupMissingLabels(context.Background(), []string{"KXNFLGAME-26SEP06DALPHI-PHI"})
	settled := domain.Settlement{Ticker: "KXNFLGAME-26SEP06DALPHI-PHI", YesQuantity: 5 * domain.Dollar}
	service.enrichSettlementLocked(&settled)
	if settled.Game != "Dallas Cowboys at Philadelphia Eagles" || settled.Outcome != "Philadelphia Eagles" {
		t.Fatalf("label naming wrong: %+v", settled)
	}
	persisted, err := store.LoadMarketLabels(context.Background())
	if err != nil || persisted["KXNFLGAME-26SEP06DALPHI-PHI"].Title == "" {
		t.Fatalf("label was not persisted: %+v err=%v", persisted, err)
	}

	// Unknown market: decoded from the ticker, and never invents a line.
	unknown := domain.Order{Ticker: "KXNCAAFTOTAL-26SEP05ECUALA-53", Side: "no"}
	service.enrichOrderLocked(&unknown)
	if unknown.Game != "College Football · Sep 5 · ECUALA" || unknown.Outcome != "Under" || unknown.Market != "Total" {
		t.Fatalf("ticker fallback wrong: %+v", unknown)
	}

	// A failed lookup is not retried on the next pass.
	service.lookupMissingLabels(context.Background(), []string{"KXNBAGAME-26SEP07LALBOS-LAL"})
	service.lookupMissingLabels(context.Background(), []string{"KXNBAGAME-26SEP07LALBOS-LAL"})
	attempts := 0
	for _, ticker := range adapter.described {
		if ticker == "KXNBAGAME-26SEP07LALBOS-LAL" {
			attempts++
		}
	}
	if attempts != 1 {
		t.Fatalf("failed lookup was attempted %d times, want 1", attempts)
	}
}
