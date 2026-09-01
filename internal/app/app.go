package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
	"github.com/davidchurgin-cpu/pmbattle/internal/live"
	"github.com/davidchurgin-cpu/pmbattle/internal/mapping"
	orderengine "github.com/davidchurgin-cpu/pmbattle/internal/orders"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
	"github.com/davidchurgin-cpu/pmbattle/internal/schedule"
	"github.com/davidchurgin-cpu/pmbattle/internal/storage"
)

type Config struct {
	ScheduleURL         string
	ScheduleInterval    time.Duration
	ExchangeEnvironment string
	Simulated           bool
	DemoTrading         bool
}

type Service struct {
	mu              sync.RWMutex
	cfg             Config
	store           *storage.Store
	schedule        schedule.Client
	exchange        exchange.Adapter
	orderEngine     *orderengine.Engine
	books           *live.Books
	allEvents       []domain.CanonicalEvent
	preferences     domain.Preferences
	snapshot        domain.Snapshot
	subscribers     map[chan domain.StreamEvent]struct{}
	restartExchange chan struct{}
	bookRequests    chan struct{}
	availableBooks  map[string]bool
	activeBook      string
}

func New(cfg Config, store *storage.Store, adapter exchange.Adapter) *Service {
	s := &Service{cfg: cfg, store: store, schedule: schedule.Client{URL: cfg.ScheduleURL}, exchange: adapter, orderEngine: orderengine.New(cfg.DemoTrading, adapter), books: live.NewBooks(), subscribers: make(map[chan domain.StreamEvent]struct{}), restartExchange: make(chan struct{}, 1), bookRequests: make(chan struct{}, 1), availableBooks: make(map[string]bool)}
	mode := "live"
	if cfg.Simulated {
		mode = "simulated"
	} else if strings.EqualFold(cfg.ExchangeEnvironment, "demo") {
		mode = "demo"
	}
	s.snapshot.Health = domain.Health{Status: "starting", Mode: mode, ExchangeState: "disconnected", TradingEnabled: cfg.DemoTrading}
	s.snapshot.ParentOrders = make([]domain.ParentOrder, 0)
	return s
}

func (s *Service) Run(ctx context.Context) {
	if value, ok, err := s.store.GetSetting(ctx, "preferences"); err == nil && ok {
		_ = json.Unmarshal([]byte(value), &s.preferences)
	}
	if parents, err := s.store.LoadParentOrders(ctx); err != nil {
		slog.Error("load parent orders", "error", err)
	} else {
		s.orderEngine.Restore(parents)
		s.mu.Lock()
		s.snapshot.ParentOrders = parents
		s.recalculateParentRiskLocked()
		s.mu.Unlock()
	}
	if cached, err := s.store.LoadEvents(ctx); err == nil && len(cached) > 0 {
		s.setEvents(cached, false)
	}
	s.refreshSchedule(ctx)
	go s.scheduleLoop(ctx)
	go s.exchangeManager(ctx)
	go s.bookManager(ctx)
	<-ctx.Done()
}

func (s *Service) UpdatePreferences(ctx context.Context, enabled []string, excludeAddedGames bool) (domain.Snapshot, error) {
	clean := make([]string, 0, len(enabled))
	seen := map[string]bool{}
	for _, sport := range enabled {
		sport = SportKey(sport)
		if sport != "" && !seen[sport] {
			seen[sport] = true
			clean = append(clean, sport)
		}
	}
	sort.Strings(clean)
	preferences := domain.Preferences{EnabledSports: clean, ExcludeAddedGames: excludeAddedGames}
	payload, err := json.Marshal(preferences)
	if err != nil {
		return domain.Snapshot{}, err
	}
	if err := s.store.SetSetting(ctx, "preferences", string(payload)); err != nil {
		return domain.Snapshot{}, err
	}
	s.mu.Lock()
	s.preferences = preferences
	events := filterEvents(s.allEvents, preferences)
	s.snapshot.Events = events
	s.snapshot.Settings = buildSettings(s.allEvents, preferences)
	if s.cfg.Simulated {
		seedAccount(&s.snapshot)
	}
	snapshot := s.snapshot
	s.mu.Unlock()
	s.broadcast(domain.StreamEvent{Type: "schedule", Data: events})
	s.broadcast(domain.StreamEvent{Type: "settings", Data: snapshot.Settings})
	select {
	case s.restartExchange <- struct{}{}:
	default:
	}
	return s.Snapshot(), nil
}

func (s *Service) Snapshot() domain.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, _ := json.Marshal(s.snapshot)
	var copy domain.Snapshot
	_ = json.Unmarshal(data, &copy)
	return copy
}

func (s *Service) Book(ticker string) (domain.OrderBook, bool) { return s.books.Get(ticker) }

type CreateParentOrderInput struct {
	EventID           string
	Ticker            string
	Rotation          string
	Outcome           string
	Market            string
	Side              string
	Strategy          string
	Policy            string
	CashRisk          domain.Money
	PriceCapMoneyline int64
	LimitPrice        domain.Money
	SliceQuantity     domain.Money
}

var ErrInvalidCancelScope = errors.New("invalid cancel scope")

type CancelScopeInput struct {
	Scope string `json:"scope"`
	Value string `json:"value,omitempty"`
}

type CancelFailure struct {
	ParentID string `json:"parentId"`
	Error    string `json:"error"`
}

type CancelScopeResult struct {
	Scope    string               `json:"scope"`
	Value    string               `json:"value,omitempty"`
	Matched  int                  `json:"matched"`
	Canceled []domain.ParentOrder `json:"canceled"`
	Failures []CancelFailure      `json:"failures"`
}

func (s *Service) CreateParentOrder(ctx context.Context, input CreateParentOrderInput) (domain.ParentOrder, error) {
	s.mu.RLock()
	tradingEnabled := s.snapshot.Health.TradingEnabled
	available := s.availableBooks[input.Ticker]
	event, quote, marketLabel, rotation, found := s.resolveOrderSelection(input.EventID, input.Ticker, input.Side)
	s.mu.RUnlock()
	if !tradingEnabled {
		return domain.ParentOrder{}, orderengine.ErrDisabled
	}
	if !available || !found {
		return domain.ParentOrder{}, orderengine.ErrInvalidOrder
	}
	book, ok := s.books.Get(input.Ticker)
	if !ok || book.Stale {
		return domain.ParentOrder{}, errors.New("order book is not synchronized")
	}
	if strings.EqualFold(strings.TrimSpace(input.Strategy), "follow") {
		price, available := followPrice(book, input.Side)
		if !available {
			return domain.ParentOrder{}, errors.New("the selected side has no resting bid to follow")
		}
		input.LimitPrice = price
		input.Policy = "post_only"
	}
	request := orderengine.CreateRequest{
		EventID: event.ID, Ticker: input.Ticker, Rotation: rotation, Outcome: quote.Outcome,
		Market: marketLabel, Side: input.Side, Strategy: input.Strategy, Policy: input.Policy,
		CashRisk: input.CashRisk, PriceCapMoneyline: input.PriceCapMoneyline, LimitPrice: input.LimitPrice,
		SliceQuantity: input.SliceQuantity,
	}
	_ = s.store.Audit(ctx, "parent_order_request", request)
	parent, child, err := s.orderEngine.Create(ctx, request)
	if err != nil {
		_ = s.store.Audit(ctx, "parent_order_rejected", map[string]any{"request": request, "error": err.Error()})
		return domain.ParentOrder{}, err
	}
	child.Rotation = parent.Rotation
	child.Market = parent.Market
	s.mu.Lock()
	s.snapshot.ParentOrders = append([]domain.ParentOrder{parent}, s.snapshot.ParentOrders...)
	s.upsertOrderLocked(child)
	s.recalculateParentRiskLocked()
	s.mu.Unlock()
	if err := s.store.SaveParentOrder(ctx, parent); err != nil {
		slog.Error("persist acknowledged parent order", "parent_id", parent.ID, "error", err)
	}
	_ = s.store.Audit(ctx, "parent_order_acknowledged", map[string]any{"parent": parent, "child": child})
	s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
	s.broadcast(domain.StreamEvent{Type: "order", Data: child})
	if parent.Strategy == "follow" {
		s.queueBookRefresh()
	}
	return parent, nil
}

func (s *Service) CancelParentOrder(ctx context.Context, id string) (domain.ParentOrder, error) {
	if !s.Snapshot().Health.TradingEnabled {
		return domain.ParentOrder{}, orderengine.ErrDisabled
	}
	parent, err := s.orderEngine.Cancel(ctx, id)
	if err != nil {
		if parent.ID != "" {
			s.mu.Lock()
			s.upsertParentLocked(parent)
			s.recalculateParentRiskLocked()
			s.mu.Unlock()
			if saveErr := s.store.SaveParentOrder(ctx, parent); saveErr != nil {
				slog.Error("persist partially canceled parent order", "parent_id", parent.ID, "error", saveErr)
			}
			s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
		}
		_ = s.store.Audit(ctx, "parent_order_cancel_failed", map[string]any{"id": id, "error": err.Error()})
		return domain.ParentOrder{}, err
	}
	s.mu.Lock()
	updatedChildren := make([]domain.Order, 0, len(parent.ChildOrderIDs))
	for i := range s.snapshot.ParentOrders {
		if s.snapshot.ParentOrders[i].ID == parent.ID {
			s.snapshot.ParentOrders[i] = parent
		}
	}
	for i := range s.snapshot.Orders {
		for _, childID := range parent.ChildOrderIDs {
			if s.snapshot.Orders[i].ID == childID {
				s.snapshot.Orders[i].Status = "canceled"
				s.snapshot.Orders[i].CashRisk = 0
				updatedChildren = append(updatedChildren, s.snapshot.Orders[i])
			}
		}
	}
	s.recalculateParentRiskLocked()
	s.mu.Unlock()
	if err := s.store.SaveParentOrder(ctx, parent); err != nil {
		slog.Error("persist canceled parent order", "parent_id", parent.ID, "error", err)
	}
	_ = s.store.Audit(ctx, "parent_order_canceled", parent)
	s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
	for _, child := range updatedChildren {
		s.broadcast(domain.StreamEvent{Type: "order", Data: child})
	}
	if parent.Strategy == "follow" {
		s.queueBookRefresh()
	}
	return parent, nil
}

// CancelParentOrders applies the same guarded parent cancellation path to a
// narrowly defined group. Each acknowledged parent is persisted and published
// immediately; failures are returned explicitly instead of hiding partial work.
func (s *Service) CancelParentOrders(ctx context.Context, input CancelScopeInput) (CancelScopeResult, error) {
	if !s.Snapshot().Health.TradingEnabled {
		return CancelScopeResult{}, orderengine.ErrDisabled
	}
	input.Scope = strings.ToLower(strings.TrimSpace(input.Scope))
	input.Value = strings.TrimSpace(input.Value)
	if !validCancelScope(input) {
		return CancelScopeResult{}, ErrInvalidCancelScope
	}
	parents := s.orderEngine.List()
	sort.Slice(parents, func(i, j int) bool {
		if parents[i].CreatedAt.Equal(parents[j].CreatedAt) {
			return parents[i].ID < parents[j].ID
		}
		return parents[i].CreatedAt.Before(parents[j].CreatedAt)
	})
	targets := make([]domain.ParentOrder, 0)
	for _, parent := range parents {
		if !parentOrderTerminal(parent.Status) && cancelScopeMatches(parent, input) {
			targets = append(targets, parent)
		}
	}
	result := CancelScopeResult{Scope: input.Scope, Value: input.Value, Matched: len(targets), Canceled: make([]domain.ParentOrder, 0, len(targets)), Failures: make([]CancelFailure, 0)}
	_ = s.store.Audit(ctx, "parent_order_bulk_cancel_request", map[string]any{"scope": input, "matched": len(targets)})
	for _, target := range targets {
		parent, err := s.CancelParentOrder(ctx, target.ID)
		if err != nil {
			result.Failures = append(result.Failures, CancelFailure{ParentID: target.ID, Error: err.Error()})
			continue
		}
		result.Canceled = append(result.Canceled, parent)
	}
	_ = s.store.Audit(ctx, "parent_order_bulk_cancel_result", result)
	s.queueBookRefresh()
	return result, nil
}

func validCancelScope(input CancelScopeInput) bool {
	switch input.Scope {
	case "all":
		return input.Value == ""
	case "event", "exchange":
		return input.Value != ""
	case "strategy":
		value := strings.ToLower(input.Value)
		return value == "basic" || value == "iceberg" || value == "follow"
	default:
		return false
	}
}

func cancelScopeMatches(parent domain.ParentOrder, input CancelScopeInput) bool {
	switch input.Scope {
	case "all":
		return true
	case "event":
		return parent.EventID == input.Value
	case "strategy":
		return strings.EqualFold(parent.Strategy, input.Value)
	case "exchange":
		return strings.EqualFold(parent.Exchange, input.Value)
	default:
		return false
	}
}

func (s *Service) resolveOrderSelection(eventID, ticker, side string) (domain.CanonicalEvent, domain.PriceQuote, string, string, bool) {
	for _, event := range s.snapshot.Events {
		if event.ID != eventID {
			continue
		}
		for _, market := range event.Markets {
			quotes := []*domain.PriceQuote{market.Away, market.Home, market.Over, market.Under}
			for _, option := range market.Options {
				quotes = append(quotes, option.Away, option.Home, option.Over, option.Under)
			}
			for _, quote := range quotes {
				if quote == nil || quote.Ticker != ticker || quote.Side != side {
					continue
				}
				rotation := ""
				for _, participant := range event.Participants {
					if participant.Name == quote.Outcome {
						rotation = participant.Rotation
					}
				}
				label := string(market.Type)
				if market.Line != "" {
					label += " " + market.Line
				}
				return event, *quote, label, rotation, true
			}
		}
	}
	return domain.CanonicalEvent{}, domain.PriceQuote{}, "", "", false
}

func (s *Service) upsertOrderLocked(order domain.Order) {
	for i := range s.snapshot.Orders {
		if s.snapshot.Orders[i].ID == order.ID {
			s.snapshot.Orders[i] = order
			return
		}
	}
	s.snapshot.Orders = append([]domain.Order{order}, s.snapshot.Orders...)
}

func (s *Service) upsertParentLocked(parent domain.ParentOrder) {
	for i := range s.snapshot.ParentOrders {
		if s.snapshot.ParentOrders[i].ID == parent.ID {
			s.snapshot.ParentOrders[i] = parent
			return
		}
	}
	s.snapshot.ParentOrders = append([]domain.ParentOrder{parent}, s.snapshot.ParentOrders...)
}

func (s *Service) recalculateParentRiskLocked() {
	s.snapshot.AtRisk = 0
	for _, parent := range s.snapshot.ParentOrders {
		s.snapshot.AtRisk += parent.FilledRisk
		if parent.Status != "canceled" && parent.Status != "filled" && parent.Status != "rejected" {
			s.snapshot.AtRisk += parent.ReservedRisk
		}
	}
}

// RequestBook selects the UI book. Active follow strategies remain subscribed
// even when their game is collapsed or another game is selected.
func (s *Service) RequestBook(ticker string) bool {
	s.mu.Lock()
	allowed := s.availableBooks[ticker]
	active := s.activeBook
	simulated := s.cfg.Simulated
	if !allowed {
		s.mu.Unlock()
		return false
	}
	s.activeBook = ticker
	s.mu.Unlock()
	if active != ticker {
		if simulated {
			s.seedSimulatedBook(ticker)
		} else {
			s.queueBookRefresh()
		}
	}
	return true
}

func (s *Service) ReleaseBook(ticker string) {
	s.mu.Lock()
	if s.activeBook == ticker {
		s.activeBook = ""
		s.mu.Unlock()
		s.queueBookRefresh()
		return
	}
	s.mu.Unlock()
}

func (s *Service) queueBookRefresh() {
	select {
	case s.bookRequests <- struct{}{}:
	default:
	}
}

func (s *Service) Subscribe() (chan domain.StreamEvent, func()) {
	ch := make(chan domain.StreamEvent, 128)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
}

func (s *Service) broadcast(event domain.StreamEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Service) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.ScheduleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshSchedule(ctx)
		}
	}
}

func (s *Service) refreshSchedule(ctx context.Context) {
	events, err := s.schedule.Fetch(ctx)
	if err != nil {
		slog.Warn("schedule refresh failed", "error", err)
		s.mu.Lock()
		s.snapshot.Health.Status = "degraded"
		s.mu.Unlock()
		return
	}
	if s.cfg.Simulated {
		attachSimulatedMarkets(events)
	}
	s.setEvents(events, true)
	if err := s.store.SaveEvents(ctx, events); err != nil {
		slog.Error("save schedule", "error", err)
	}
}

func (s *Service) setEvents(events []domain.CanonicalEvent, publish bool) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].StartTime.Equal(events[j].StartTime) {
			return firstRotation(events[i]) < firstRotation(events[j])
		}
		return events[i].StartTime.Before(events[j].StartTime)
	})
	s.mu.Lock()
	if !s.cfg.Simulated {
		existingMarkets := make(map[string][]domain.MarketView, len(s.snapshot.Events))
		for _, event := range s.snapshot.Events {
			if hasLiveMarkets(event.Markets) {
				existingMarkets[event.ID] = event.Markets
			}
		}
		for i := range events {
			if markets := existingMarkets[events[i].ID]; len(markets) > 0 {
				events[i].Markets = markets
			}
		}
	}
	s.allEvents = events
	s.snapshot.Events = filterEvents(events, s.preferences)
	s.snapshot.Settings = buildSettings(events, s.preferences)
	s.snapshot.Health.Status = "ok"
	s.snapshot.Health.ScheduleUpdated = time.Now().UTC()
	if s.cfg.Simulated {
		seedAccount(&s.snapshot)
	}
	visible := append([]domain.CanonicalEvent(nil), s.snapshot.Events...)
	s.mu.Unlock()
	if s.cfg.Simulated {
		s.setAvailableBooks(eventTickers(visible))
	}
	if publish {
		s.broadcast(domain.StreamEvent{Type: "schedule", Data: s.Snapshot().Events})
	}
}

func hasLiveMarkets(markets []domain.MarketView) bool {
	for _, market := range markets {
		for _, quote := range []*domain.PriceQuote{market.Away, market.Home, market.Over, market.Under} {
			if quote != nil && quote.Ticker != "" && !strings.HasPrefix(quote.Ticker, "SIM-") {
				return true
			}
		}
	}
	return false
}

func (s *Service) exchangeManager(ctx context.Context) {
	for {
		child, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); s.exchangeLoop(child) }()
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return
		case <-s.restartExchange:
			cancel()
			<-done
		}
	}
}

func (s *Service) exchangeLoop(ctx context.Context) {
	if s.exchange == nil {
		return
	}
	s.mu.RLock()
	events := append([]domain.CanonicalEvent(nil), s.snapshot.Events...)
	s.mu.RUnlock()
	markets, err := s.exchange.ListMarkets(ctx, events)
	if err != nil {
		slog.Warn("market discovery failed", "error", err)
		return
	}
	slog.Info("market discovery complete", "schedule_events", len(events), "exchange_markets", len(markets))
	matched := mapping.Match(events, markets)
	accepted := 0
	for _, market := range matched {
		if market.MappingStatus == "accepted" {
			accepted++
			_ = s.store.SaveMapping(ctx, market)
		}
	}
	tickers := make([]string, 0)
	if !s.cfg.Simulated {
		tickers = s.attachMatched(matched)
	}
	s.setAvailableBooks(tickers)
	slog.Info("market mapping complete", "accepted_markets", accepted, "available_books", len(tickers))
	s.reconcileAccount(ctx, false)
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		subscription, err := s.exchange.SubscribeAccount(ctx)
		if err != nil {
			s.setExchangeHealth("disconnected", 0)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		s.setExchangeHealth("connected", 0)
		ended := false
		for !ended {
			select {
			case <-ctx.Done():
				_ = subscription.Close()
				return
			case event, ok := <-subscription.Events:
				if !ok {
					ended = true
					break
				}
				s.handleExchangeEvent(event)
			case err := <-subscription.Errors:
				if err != nil {
					slog.Warn("exchange stream", "error", err)
				}
				ended = true
			}
		}
		_ = subscription.Close()
		s.reconcileAccount(ctx, true)
		s.setExchangeHealth("reconnecting", 0)
	}
}

func (s *Service) reconcileAccount(ctx context.Context, publish bool) {
	if balance, balanceErr := s.exchange.Balance(ctx); balanceErr != nil {
		slog.Warn("balance reconciliation failed", "error", balanceErr)
	} else {
		s.mu.Lock()
		s.snapshot.Bankroll = balance
		s.mu.Unlock()
	}
	orders, positions, fills, err := s.exchange.Snapshot(ctx)
	if err != nil {
		slog.Warn("account reconciliation failed", "error", err)
	} else {
		reconciledParents := make([]domain.ParentOrder, 0)
		for _, order := range orders {
			if parent, ok := s.orderEngine.ApplyOrder(order); ok {
				reconciledParents = append(reconciledParents, parent)
			}
		}
		s.mu.Lock()
		s.snapshot.Orders = orders
		s.snapshot.Positions = positions
		for _, parent := range reconciledParents {
			s.upsertParentLocked(parent)
		}
		s.recalculateParentRiskLocked()
		s.mu.Unlock()
		for _, parent := range reconciledParents {
			if err := s.store.SaveParentOrder(ctx, parent); err != nil {
				slog.Error("persist reconciled parent order", "parent_id", parent.ID, "error", err)
			}
			if publish {
				s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
			}
		}
		if publish {
			for _, order := range orders {
				s.broadcast(domain.StreamEvent{Type: "order", Data: order})
			}
			for _, position := range positions {
				s.broadcast(domain.StreamEvent{Type: "position", Data: position})
			}
		}
		for _, fill := range fills {
			s.applyFill(fill, publish)
		}
	}
	s.reconcileParentFills(ctx, publish)
	snapshot := s.Snapshot()
	s.broadcast(domain.StreamEvent{Type: "account_snapshot", Data: domain.AccountSnapshot{ParentOrders: snapshot.ParentOrders, Orders: snapshot.Orders, Positions: snapshot.Positions, Fills: snapshot.Fills, Bankroll: snapshot.Bankroll, AtRisk: snapshot.AtRisk}})
}

func (s *Service) reconcileParentFills(ctx context.Context, publish bool) {
	parents := s.orderEngine.List()
	childIDs := make([]string, 0)
	seen := make(map[string]bool)
	for _, parent := range parents {
		for _, childID := range parent.ChildOrderIDs {
			if childID != "" && !seen[childID] {
				seen[childID] = true
				childIDs = append(childIDs, childID)
			}
		}
	}
	if len(childIDs) == 0 {
		return
	}
	fills, err := s.exchange.Fills(ctx, childIDs)
	if err != nil {
		slog.Warn("parent fill reconciliation failed", "orders", len(childIDs), "error", err)
		return
	}
	for _, fill := range fills {
		s.applyFill(fill, publish)
	}
}

func (s *Service) setAvailableBooks(tickers []string) {
	available := make(map[string]bool, len(tickers))
	for _, ticker := range tickers {
		available[ticker] = true
	}
	s.mu.Lock()
	s.availableBooks = available
	active := s.activeBook
	if active != "" && !available[active] {
		s.activeBook = ""
	}
	s.mu.Unlock()
	s.queueBookRefresh()
}

func eventTickers(events []domain.CanonicalEvent) []string {
	set := map[string]bool{}
	for _, event := range events {
		for _, market := range event.Markets {
			for _, quote := range []*domain.PriceQuote{market.Away, market.Home, market.Over, market.Under} {
				if quote != nil && quote.Ticker != "" {
					set[quote.Ticker] = true
				}
			}
		}
	}
	tickers := make([]string, 0, len(set))
	for ticker := range set {
		tickers = append(tickers, ticker)
	}
	sort.Strings(tickers)
	return tickers
}

func (s *Service) seedSimulatedBook(ticker string) {
	s.mu.Lock()
	s.activeBook = ticker
	var quote *domain.PriceQuote
	for _, event := range s.snapshot.Events {
		for _, market := range event.Markets {
			for _, candidate := range []*domain.PriceQuote{market.Away, market.Home, market.Over, market.Under} {
				if candidate != nil && candidate.Ticker == ticker {
					quote = candidate
					break
				}
			}
		}
	}
	s.mu.Unlock()
	if quote == nil {
		return
	}
	book := domain.OrderBook{Ticker: ticker, Sequence: 1}
	for step := domain.Money(1); step <= 5; step++ {
		bid := quote.RawPrice - step*100
		ask := quote.RawPrice + step*100
		if bid > 0 {
			book.Yes = append(book.Yes, domain.BookLevel{Price: bid, Quantity: (80 + step*55) * domain.Dollar})
		}
		if ask < domain.Dollar {
			book.No = append(book.No, domain.BookLevel{Price: ask, Quantity: (95 + step*47) * domain.Dollar})
		}
	}
	s.books.Snapshot(book)
	s.broadcast(domain.StreamEvent{Type: "orderbook", Data: book})
}

func (s *Service) bookManager(ctx context.Context) {
	var cancel context.CancelFunc
	currentKey := ""
	currentTickers := []string(nil)
	for {
		select {
		case <-ctx.Done():
			if cancel != nil {
				cancel()
			}
			return
		case <-s.bookRequests:
			tickers := s.desiredBookTickers()
			key := strings.Join(tickers, "\x00")
			if key == currentKey {
				continue
			}
			if cancel != nil {
				cancel()
			}
			for _, previous := range currentTickers {
				if book, ok := s.books.MarkStale(previous); ok {
					s.applyFollowBook(book)
					s.broadcast(domain.StreamEvent{Type: "book_stale", Data: book})
				}
			}
			currentKey = key
			currentTickers = tickers
			if len(tickers) > 0 && s.exchange != nil {
				var child context.Context
				child, cancel = context.WithCancel(ctx)
				go s.bookLoop(child, tickers)
			} else {
				cancel = nil
			}
		}
	}
}

func (s *Service) desiredBookTickers() []string {
	s.mu.RLock()
	set := make(map[string]bool)
	if s.activeBook != "" && s.availableBooks[s.activeBook] {
		set[s.activeBook] = true
	}
	for _, parent := range s.snapshot.ParentOrders {
		if parent.Strategy == "follow" && parent.Ticker != "" && !parentOrderTerminal(parent.Status) && s.availableBooks[parent.Ticker] {
			set[parent.Ticker] = true
		}
	}
	s.mu.RUnlock()
	tickers := make([]string, 0, len(set))
	for ticker := range set {
		tickers = append(tickers, ticker)
	}
	sort.Strings(tickers)
	return tickers
}

func (s *Service) bookLoop(ctx context.Context, tickers []string) {
	backoff := time.Second
	for ctx.Err() == nil {
		subscription, err := s.exchange.SubscribeBooks(ctx, tickers)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		ended := false
		for !ended {
			select {
			case <-ctx.Done():
				_ = subscription.Close()
				return
			case event, ok := <-subscription.Events:
				if !ok {
					ended = true
					break
				}
				s.handleExchangeEvent(event)
			case err := <-subscription.Errors:
				if err != nil {
					slog.Warn("order-book stream", "tickers", len(tickers), "error", err)
				}
				ended = true
			}
		}
		_ = subscription.Close()
		for _, ticker := range tickers {
			if book, ok := s.books.MarkStale(ticker); ok {
				s.applyFollowBook(book)
				s.broadcast(domain.StreamEvent{Type: "book_stale", Data: book})
			}
		}
	}
}

func parentOrderTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "filled", "executed", "closed", "canceled", "cancelled", "rejected":
		return true
	default:
		return false
	}
}

func (s *Service) handleExchangeEvent(event domain.StreamEvent) {
	switch event.Type {
	case "orderbook":
		if book, ok := event.Data.(domain.OrderBook); ok {
			s.books.Snapshot(book)
			s.applyFollowBook(book)
			s.broadcast(event)
		}
	case "orderbook_delta":
		if delta, ok := event.Data.(domain.OrderBookDelta); ok {
			book, err := s.books.Apply(delta)
			if err != nil {
				s.applyFollowBook(book)
				s.broadcast(domain.StreamEvent{Type: "book_stale", Data: book})
			} else {
				s.applyFollowBook(book)
				s.broadcast(domain.StreamEvent{Type: "orderbook", Data: book})
			}
		}
	case "fill":
		if fill, ok := event.Data.(domain.Fill); ok {
			s.applyFill(fill, true)
		}
	case "order":
		if order, ok := event.Data.(domain.Order); ok {
			parent, parentMatched := s.orderEngine.ApplyOrder(order)
			s.mu.Lock()
			s.upsertOrderLocked(order)
			if parentMatched {
				s.upsertParentLocked(parent)
				s.recalculateParentRiskLocked()
			}
			s.mu.Unlock()
			if parentMatched {
				if err := s.store.SaveParentOrder(context.Background(), parent); err != nil {
					slog.Error("persist order-reconciled parent order", "parent_id", parent.ID, "order_id", order.ID, "error", err)
				}
				s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
				s.queueBookRefresh()
			}
			s.broadcast(event)
		}
	case "position":
		if position, ok := event.Data.(domain.Position); ok {
			s.mu.Lock()
			replaced := false
			for i := range s.snapshot.Positions {
				if s.snapshot.Positions[i].Ticker == position.Ticker {
					s.snapshot.Positions[i] = position
					replaced = true
					break
				}
			}
			if !replaced {
				s.snapshot.Positions = append([]domain.Position{position}, s.snapshot.Positions...)
			}
			s.mu.Unlock()
			s.broadcast(event)
		}
	default:
		s.broadcast(event)
	}
}

func (s *Service) applyFollowBook(book domain.OrderBook) {
	for _, result := range s.orderEngine.HandleBook(context.Background(), book) {
		if !result.Changed || result.Parent.ID == "" {
			continue
		}
		s.mu.Lock()
		s.upsertParentLocked(result.Parent)
		if result.Order != nil {
			s.upsertOrderLocked(*result.Order)
		}
		s.recalculateParentRiskLocked()
		s.mu.Unlock()
		if err := s.store.SaveParentOrder(context.Background(), result.Parent); err != nil {
			slog.Error("persist follow decision", "parent_id", result.Parent.ID, "error", err)
		}
		audit := map[string]any{"parent": result.Parent, "book_sequence": book.Sequence}
		if result.Order != nil {
			audit["amended_order"] = result.Order
		}
		if result.Err != nil {
			audit["strategy_error"] = result.Err.Error()
			slog.Warn("follow strategy paused", "parent_id", result.Parent.ID, "error", result.Err)
		}
		_ = s.store.Audit(context.Background(), "follow_book_decision", audit)
		s.broadcast(domain.StreamEvent{Type: "parent_order", Data: result.Parent})
		if result.Order != nil {
			s.broadcast(domain.StreamEvent{Type: "order", Data: *result.Order})
		}
	}
}

func followPrice(book domain.OrderBook, side string) (domain.Money, bool) {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "yes":
		if len(book.Yes) > 0 && book.Yes[0].Price > 0 && book.Yes[0].Price < domain.Dollar {
			return book.Yes[0].Price, true
		}
	case "no":
		if len(book.No) > 0 {
			price := domain.Dollar - book.No[0].Price
			if price > 0 && price < domain.Dollar {
				return price, true
			}
		}
	}
	return 0, false
}

func (s *Service) applyFill(fill domain.Fill, publish bool) bool {
	s.mu.RLock()
	duplicate := false
	for _, existing := range s.snapshot.Fills {
		if fill.ID != "" && existing.ID == fill.ID {
			duplicate = true
			break
		}
	}
	s.mu.RUnlock()
	if duplicate {
		return false
	}
	parent, refreshedChild, reconciled, strategyErr := s.orderEngine.HandleFill(context.Background(), fill)
	if !reconciled {
		parent, _ = s.orderEngine.ParentForChild(fill.OrderID)
	}
	if parent.ID != "" {
		fill.EventID = parent.EventID
		fill.Rotation = parent.Rotation
		fill.Team = parent.Outcome
		fill.Market = parent.Market
	}
	if reconciled {
		if refreshedChild != nil {
			refreshedChild.Rotation = parent.Rotation
			refreshedChild.Market = parent.Market
		}
		s.mu.Lock()
		s.upsertParentLocked(parent)
		if refreshedChild != nil {
			s.upsertOrderLocked(*refreshedChild)
		}
		s.recalculateParentRiskLocked()
		s.mu.Unlock()
		if err := s.store.SaveParentOrder(context.Background(), parent); err != nil {
			slog.Error("persist fill-reconciled parent order", "parent_id", parent.ID, "fill_id", fill.ID, "error", err)
		}
		reconciliation := map[string]any{"parent": parent, "fill": fill}
		if refreshedChild != nil {
			reconciliation["refreshed_child"] = refreshedChild
		}
		if strategyErr != nil {
			reconciliation["strategy_error"] = strategyErr.Error()
			slog.Warn("parent strategy paused after fill", "parent_id", parent.ID, "fill_id", fill.ID, "error", strategyErr)
		}
		_ = s.store.Audit(context.Background(), "parent_order_fill_reconciled", reconciliation)
		if publish {
			s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
			if refreshedChild != nil {
				s.broadcast(domain.StreamEvent{Type: "order", Data: *refreshedChild})
			}
		}
		s.queueBookRefresh()
	}
	s.mu.Lock()
	s.snapshot.Fills = append([]domain.Fill{fill}, s.snapshot.Fills...)
	if len(s.snapshot.Fills) > 250 {
		s.snapshot.Fills = s.snapshot.Fills[:250]
	}
	s.mu.Unlock()
	_ = s.store.Audit(context.Background(), "fill", fill)
	if publish {
		s.broadcast(domain.StreamEvent{Type: "fill", Data: fill})
	}
	return true
}

func (s *Service) attachMatched(markets []domain.CanonicalMarket) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := map[string][]domain.CanonicalMarket{}
	for _, m := range markets {
		if m.MappingStatus == "accepted" {
			byID[m.EventID] = append(byID[m.EventID], m)
		}
	}
	selectedTickers := map[string]bool{}
	for i := range s.snapshot.Events {
		matches := byID[s.snapshot.Events[i].ID]
		if len(matches) == 0 {
			continue
		}
		event := &s.snapshot.Events[i]
		views := make([]domain.MarketView, 0, 3)

		moneyline := domain.MarketView{Type: domain.MarketMoneyline, Status: "open"}
		for _, market := range matches {
			if market.Type != domain.MarketMoneyline {
				continue
			}
			participant := mapping.ParticipantIndex(*event, market.Outcome+" "+market.Subtitle)
			quote := quoteForMarket(market, true)
			if participant < 0 || quote == nil {
				continue
			}
			quote.Outcome = event.Participants[participant].Name
			if participant == 0 {
				moneyline.Away = quote
			} else if participant == 1 {
				moneyline.Home = quote
			}
			selectedTickers[market.ExchangeTicker] = true
		}
		if moneyline.Away != nil || moneyline.Home != nil {
			views = append(views, moneyline)
		}

		spreadMarkets := closestMarkets(matches, domain.MarketSpread, 5)
		spreadOptions := make([]domain.MarketOption, 0, len(spreadMarkets))
		for _, market := range spreadMarkets {
			if option := spreadOption(*event, market); option != nil {
				spreadOptions = append(spreadOptions, *option)
				selectedTickers[market.ExchangeTicker] = true
			}
		}
		if len(spreadOptions) > 0 {
			primary := spreadOptions[0]
			sortedOptions := append([]domain.MarketOption(nil), spreadOptions...)
			sortMarketOptions(sortedOptions)
			views = append(views, domain.MarketView{Type: domain.MarketSpread, Line: primary.Line, Away: primary.Away, Home: primary.Home, Options: sortedOptions, Status: "open"})
		}

		totalMarkets := closestMarkets(matches, domain.MarketTotal, 5)
		totalOptions := make([]domain.MarketOption, 0, len(totalMarkets))
		for _, market := range totalMarkets {
			if option := totalOption(market); option != nil {
				totalOptions = append(totalOptions, *option)
				selectedTickers[market.ExchangeTicker] = true
			}
		}
		if len(totalOptions) > 0 {
			primary := totalOptions[0]
			sortedOptions := append([]domain.MarketOption(nil), totalOptions...)
			sortMarketOptions(sortedOptions)
			views = append(views, domain.MarketView{Type: domain.MarketTotal, Line: primary.Line, Over: primary.Over, Under: primary.Under, Options: sortedOptions, Status: "open"})
		}
		event.Markets = views
	}
	tickers := make([]string, 0, len(selectedTickers))
	for ticker := range selectedTickers {
		tickers = append(tickers, ticker)
	}
	sort.Strings(tickers)
	return tickers
}

func quoteForMarket(market domain.CanonicalMarket, yes bool) *domain.PriceQuote {
	price, quantity := market.YesAsk, market.YesAskSize
	if !yes {
		price, quantity = domain.Dollar-market.YesBid, market.YesBidSize
	}
	if quantity <= 0 {
		quantity = domain.Dollar
	}
	quote, err := pricing.Quote(price, quantity, false)
	if err != nil {
		return nil
	}
	quote.Exchange = market.Exchange
	quote.Ticker = market.ExchangeTicker
	if yes {
		quote.Side = "yes"
	} else {
		quote.Side = "no"
	}
	return &quote
}

func closestMarkets(markets []domain.CanonicalMarket, marketType domain.MarketType, limit int) []domain.CanonicalMarket {
	candidates := make([]domain.CanonicalMarket, 0)
	for i := range markets {
		market := markets[i]
		if market.Type != marketType || market.Line == "" || market.YesAsk <= 0 || market.YesAsk >= domain.Dollar {
			continue
		}
		candidates = append(candidates, market)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return marketDistance(candidates[i]) < marketDistance(candidates[j]) })
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func marketDistance(market domain.CanonicalMarket) int64 {
	mid := int64(market.YesAsk)
	if market.YesBid > 0 {
		mid = (int64(market.YesBid) + int64(market.YesAsk)) / 2
	}
	distance := mid - int64(domain.Dollar/2)
	if distance < 0 {
		return -distance
	}
	return distance
}

func spreadOption(event domain.CanonicalEvent, market domain.CanonicalMarket) *domain.MarketOption {
	participant := mapping.ParticipantIndex(event, market.Outcome+" "+market.Subtitle)
	yesQuote, noQuote := quoteForMarket(market, true), quoteForMarket(market, false)
	if participant < 0 || yesQuote == nil || noQuote == nil {
		return nil
	}
	option := &domain.MarketOption{}
	line := strings.TrimPrefix(market.Line, "+")
	if participant == 0 {
		option.Away, option.Home, option.Line = yesQuote, noQuote, "+"+line
	} else {
		option.Away, option.Home, option.Line = noQuote, yesQuote, "-"+line
	}
	option.Away.Outcome = event.Participants[0].Name
	option.Home.Outcome = event.Participants[1].Name
	return option
}

func totalOption(market domain.CanonicalMarket) *domain.MarketOption {
	over, under := quoteForMarket(market, true), quoteForMarket(market, false)
	if over == nil || under == nil {
		return nil
	}
	over.Outcome, under.Outcome = "Over", "Under"
	return &domain.MarketOption{Line: market.Line, Over: over, Under: under}
}

func sortMarketOptions(options []domain.MarketOption) {
	sort.SliceStable(options, func(i, j int) bool {
		left, _ := strconv.ParseFloat(options[i].Line, 64)
		right, _ := strconv.ParseFloat(options[j].Line, 64)
		return left < right
	})
}

func (s *Service) setExchangeHealth(state string, latency int64) {
	s.mu.Lock()
	s.snapshot.Health.ExchangeState = state
	s.snapshot.Health.LatencyMS = latency
	s.mu.Unlock()
	s.broadcast(domain.StreamEvent{Type: "health", Data: s.snapshot.Health})
}

func firstRotation(event domain.CanonicalEvent) string {
	if len(event.Participants) == 0 {
		return ""
	}
	return event.Participants[0].Rotation
}

func attachSimulatedMarkets(events []domain.CanonicalEvent) {
	spreadLines := []string{"-1.5", "-2.5", "-3.5", "-5.5", "-6.5", "+1.5", "+2.5", "+3.5"}
	totalLines := []string{"41.5", "42.5", "44.5", "46.5", "48.5", "51.5", "54.5"}
	for i := range events {
		if len(events[i].Participants) < 2 {
			continue
		}
		awayName := events[i].Participants[0].Name
		homeName := events[i].Participants[1].Name
		prefix := "SIM-" + events[i].ID
		limitScale := domain.Money(1)
		if isAddedGame(events[i]) {
			limitScale = 5
		}

		moneylinePrice := domain.Money(4400 + ((i * 137) % 1200))
		awayMoneyline := simulatedQuote(moneylinePrice, 1800*domain.Dollar/limitScale, prefix+"-ML-A", awayName)
		homeMoneyline := simulatedQuote(domain.Dollar-moneylinePrice, 2200*domain.Dollar/limitScale, prefix+"-ML-H", homeName)

		spreadPrice := domain.Money(4700 + ((i * 83) % 700))
		awaySpread := simulatedQuote(spreadPrice, 1400*domain.Dollar/limitScale, prefix+"-SP-A", awayName)
		homeSpread := simulatedQuote(domain.Dollar-spreadPrice, 1600*domain.Dollar/limitScale, prefix+"-SP-H", homeName)

		totalPrice := domain.Money(4700 + ((i * 61) % 700))
		over := simulatedQuote(totalPrice, 1200*domain.Dollar/limitScale, prefix+"-TO-O", "Over")
		under := simulatedQuote(domain.Dollar-totalPrice, 1500*domain.Dollar/limitScale, prefix+"-TO-U", "Under")

		events[i].Markets = []domain.MarketView{
			{Type: domain.MarketMoneyline, Away: awayMoneyline, Home: homeMoneyline, Status: "open"},
			{Type: domain.MarketSpread, Line: spreadLines[i%len(spreadLines)], Away: awaySpread, Home: homeSpread, Status: "open"},
			{Type: domain.MarketTotal, Line: totalLines[i%len(totalLines)], Over: over, Under: under, Status: "open"},
		}
	}
}

func simulatedQuote(price, quantity domain.Money, ticker, outcome string) *domain.PriceQuote {
	quote, err := pricing.Quote(price, quantity, false)
	if err != nil {
		return nil
	}
	quote.Exchange = "Kalshi"
	quote.Ticker = ticker
	quote.Outcome = outcome
	quote.Side = "yes"
	return &quote
}

func seedAccount(snapshot *domain.Snapshot) {
	if len(snapshot.Orders) > 0 || len(snapshot.Events) == 0 {
		return
	}
	now := time.Now().UTC()
	event := snapshot.Events[0]
	if len(event.Participants) < 2 {
		return
	}
	snapshot.Bankroll = 18420 * domain.Dollar
	snapshot.AtRisk = 3875 * domain.Dollar
	snapshot.Orders = []domain.Order{{ID: "sim-order-1", Exchange: "Kalshi", Ticker: "SIM-" + event.ID + "-H", Rotation: event.Participants[1].Rotation, Market: event.Participants[1].Name + " moneyline", Side: "yes", Status: "working", Quantity: 2500 * domain.Dollar, LimitPrice: 5800, CashRisk: 1450 * domain.Dollar, CreatedAt: now.Add(-3 * time.Minute)}}
	snapshot.Fills = []domain.Fill{{ID: "sim-fill-1", Exchange: "Kalshi", Ticker: "SIM-" + event.ID + "-H", EventID: event.ID, Rotation: event.Participants[1].Rotation, Team: event.Participants[1].Name, Market: "moneyline", Side: "yes", Quantity: 1000 * domain.Dollar, RawPrice: 5700, AllInMoneyline: -138, Fee: 28 * domain.Dollar, CashRisk: 598 * domain.Dollar, CreatedAt: now.Add(-time.Minute)}}
	snapshot.Positions = []domain.Position{{Exchange: "Kalshi", Ticker: "SIM-" + event.ID + "-H", Rotation: event.Participants[1].Rotation, Market: event.Participants[1].Name + " moneyline", Quantity: 1000 * domain.Dollar, CashRisk: 598 * domain.Dollar, AveragePrice: 5700, CurrentPrice: 5800, UnrealizedPnL: 10 * domain.Dollar}}
}

func SportKey(value string) string { return strings.ToUpper(strings.TrimSpace(value)) }

func filterEvents(events []domain.CanonicalEvent, preferences domain.Preferences) []domain.CanonicalEvent {
	enabled := map[string]bool{}
	for _, sport := range preferences.EnabledSports {
		enabled[SportKey(sport)] = true
	}
	filtered := make([]domain.CanonicalEvent, 0, len(events))
	for _, event := range events {
		if preferences.ExcludeAddedGames && isAddedGame(event) {
			continue
		}
		if preferences.EnabledSports == nil || enabled[SportKey(event.Sport)] {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func buildSettings(events []domain.CanonicalEvent, preferences domain.Preferences) domain.Settings {
	counts := map[string]int{}
	addedCounts := map[string]int{}
	for _, event := range events {
		name := SportKey(event.Sport)
		counts[name]++
		if isAddedGame(event) {
			addedCounts[name]++
		}
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	enabled := map[string]bool{}
	all := preferences.EnabledSports == nil
	for _, sport := range preferences.EnabledSports {
		enabled[SportKey(sport)] = true
	}
	options := make([]domain.SportOption, 0, len(names))
	for _, name := range names {
		options = append(options, domain.SportOption{Name: name, EventCount: counts[name], AddedGameCount: addedCounts[name], Enabled: all || enabled[name]})
	}
	return domain.Settings{Preferences: preferences, AvailableSports: options}
}

func isAddedGame(event domain.CanonicalEvent) bool {
	if len(event.ID) != 6 {
		return false
	}
	for _, character := range event.ID {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
