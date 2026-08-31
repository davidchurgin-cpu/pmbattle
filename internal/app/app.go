package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
	"github.com/davidchurgin-cpu/pmbattle/internal/live"
	"github.com/davidchurgin-cpu/pmbattle/internal/mapping"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
	"github.com/davidchurgin-cpu/pmbattle/internal/schedule"
	"github.com/davidchurgin-cpu/pmbattle/internal/storage"
)

type Config struct {
	ScheduleURL      string
	ScheduleInterval time.Duration
	Simulated        bool
}

type Service struct {
	mu          sync.RWMutex
	cfg         Config
	store       *storage.Store
	schedule    schedule.Client
	exchange    exchange.Adapter
	books       *live.Books
	snapshot    domain.Snapshot
	subscribers map[chan domain.StreamEvent]struct{}
}

func New(cfg Config, store *storage.Store, adapter exchange.Adapter) *Service {
	s := &Service{cfg: cfg, store: store, schedule: schedule.Client{URL: cfg.ScheduleURL}, exchange: adapter, books: live.NewBooks(), subscribers: make(map[chan domain.StreamEvent]struct{})}
	s.snapshot.Health = domain.Health{Status: "starting", Mode: map[bool]string{true: "simulated", false: "live"}[cfg.Simulated], ExchangeState: "disconnected", TradingEnabled: false}
	return s
}

func (s *Service) Run(ctx context.Context) {
	if cached, err := s.store.LoadEvents(ctx); err == nil && len(cached) > 0 {
		s.setEvents(cached, false)
	}
	s.refreshSchedule(ctx)
	go s.scheduleLoop(ctx)
	go s.exchangeLoop(ctx)
	<-ctx.Done()
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
	s.snapshot.Events = events
	s.snapshot.Health.Status = "ok"
	s.snapshot.Health.ScheduleUpdated = time.Now().UTC()
	if s.cfg.Simulated {
		seedAccount(&s.snapshot)
	}
	s.mu.Unlock()
	if publish {
		s.broadcast(domain.StreamEvent{Type: "schedule", Data: events})
	}
}

func (s *Service) exchangeLoop(ctx context.Context) {
	if s.exchange == nil {
		return
	}
	markets, err := s.exchange.ListMarkets(ctx)
	if err != nil {
		slog.Warn("market discovery failed", "error", err)
		return
	}
	s.mu.RLock()
	events := append([]domain.CanonicalEvent(nil), s.snapshot.Events...)
	s.mu.RUnlock()
	matched := mapping.Match(events, markets)
	tickers := make([]string, 0)
	for _, market := range matched {
		if market.MappingStatus == "accepted" {
			tickers = append(tickers, market.ExchangeTicker)
			_ = s.store.SaveMapping(ctx, market)
		}
	}
	if !s.cfg.Simulated {
		s.attachMatched(matched)
	}
	orders, positions, fills, err := s.exchange.Snapshot(ctx)
	if err == nil {
		s.mu.Lock()
		s.snapshot.Orders = orders
		s.snapshot.Positions = positions
		s.snapshot.Fills = fills
		s.mu.Unlock()
	}
	if len(tickers) == 0 {
		return
	}
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		subscription, err := s.exchange.Subscribe(ctx, tickers)
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
		s.setExchangeHealth("reconnecting", 0)
	}
}

func (s *Service) handleExchangeEvent(event domain.StreamEvent) {
	switch event.Type {
	case "orderbook":
		if book, ok := event.Data.(domain.OrderBook); ok {
			s.books.Snapshot(book)
			s.broadcast(event)
		}
	case "orderbook_delta":
		if delta, ok := event.Data.(domain.OrderBookDelta); ok {
			book, err := s.books.Apply(delta)
			if err != nil {
				s.broadcast(domain.StreamEvent{Type: "book_stale", Data: book})
			} else {
				s.broadcast(domain.StreamEvent{Type: "orderbook", Data: book})
			}
		}
	case "fill":
		if fill, ok := event.Data.(domain.Fill); ok {
			s.mu.Lock()
			s.snapshot.Fills = append([]domain.Fill{fill}, s.snapshot.Fills...)
			if len(s.snapshot.Fills) > 250 {
				s.snapshot.Fills = s.snapshot.Fills[:250]
			}
			s.mu.Unlock()
			_ = s.store.Audit(context.Background(), "fill", fill)
			s.broadcast(event)
		}
	case "order":
		if order, ok := event.Data.(domain.Order); ok {
			s.mu.Lock()
			replaced := false
			for i := range s.snapshot.Orders {
				if s.snapshot.Orders[i].ID == order.ID {
					s.snapshot.Orders[i] = order
					replaced = true
					break
				}
			}
			if !replaced {
				s.snapshot.Orders = append([]domain.Order{order}, s.snapshot.Orders...)
			}
			s.mu.Unlock()
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

func (s *Service) attachMatched(markets []domain.CanonicalMarket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := map[string][]domain.CanonicalMarket{}
	for _, m := range markets {
		if m.MappingStatus == "accepted" {
			byID[m.EventID] = append(byID[m.EventID], m)
		}
	}
	for i := range s.snapshot.Events {
		matches := byID[s.snapshot.Events[i].ID]
		if len(matches) == 0 {
			continue
		}
		m := matches[0]
		quote, err := pricing.Quote(m.YesAsk, domain.Dollar, false)
		if err != nil {
			continue
		}
		quote.Exchange = m.Exchange
		quote.Ticker = m.ExchangeTicker
		quote.Outcome = m.Outcome
		s.snapshot.Events[i].Markets = []domain.MarketView{{Type: domain.MarketMoneyline, Home: &quote, Status: "open"}}
	}
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
	for i := range events {
		if len(events[i].Participants) < 2 {
			continue
		}
		base := domain.Money(4400 + ((i * 137) % 1200))
		away, _ := pricing.Quote(base, domain.Money(1800*domain.Dollar), false)
		home, _ := pricing.Quote(domain.Dollar-base, domain.Money(2200*domain.Dollar), false)
		away.Exchange = "Kalshi"
		away.Ticker = "SIM-" + events[i].ID + "-A"
		away.Outcome = events[i].Participants[0].Name
		home.Exchange = "Kalshi"
		home.Ticker = "SIM-" + events[i].ID + "-H"
		home.Outcome = events[i].Participants[1].Name
		events[i].Markets = []domain.MarketView{{Type: domain.MarketMoneyline, Away: &away, Home: &home, Status: "open"}}
	}
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
