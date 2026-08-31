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
	mu              sync.RWMutex
	cfg             Config
	store           *storage.Store
	schedule        schedule.Client
	exchange        exchange.Adapter
	books           *live.Books
	allEvents       []domain.CanonicalEvent
	preferences     domain.Preferences
	snapshot        domain.Snapshot
	subscribers     map[chan domain.StreamEvent]struct{}
	restartExchange chan struct{}
}

func New(cfg Config, store *storage.Store, adapter exchange.Adapter) *Service {
	s := &Service{cfg: cfg, store: store, schedule: schedule.Client{URL: cfg.ScheduleURL}, exchange: adapter, books: live.NewBooks(), subscribers: make(map[chan domain.StreamEvent]struct{}), restartExchange: make(chan struct{}, 1)}
	s.snapshot.Health = domain.Health{Status: "starting", Mode: map[bool]string{true: "simulated", false: "live"}[cfg.Simulated], ExchangeState: "disconnected", TradingEnabled: false}
	return s
}

func (s *Service) Run(ctx context.Context) {
	if value, ok, err := s.store.GetSetting(ctx, "preferences"); err == nil && ok {
		_ = json.Unmarshal([]byte(value), &s.preferences)
	}
	if cached, err := s.store.LoadEvents(ctx); err == nil && len(cached) > 0 {
		s.setEvents(cached, false)
	}
	s.refreshSchedule(ctx)
	go s.scheduleLoop(ctx)
	go s.exchangeManager(ctx)
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
	s.mu.Unlock()
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
	slog.Info("market mapping complete", "accepted_markets", accepted, "subscribed_books", len(tickers))
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
		s.books.MarkAllStale()
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

		if spread := closestMarket(matches, domain.MarketSpread); spread != nil {
			participant := mapping.ParticipantIndex(*event, spread.Outcome+" "+spread.Subtitle)
			yesQuote, noQuote := quoteForMarket(*spread, true), quoteForMarket(*spread, false)
			if participant >= 0 && yesQuote != nil && noQuote != nil {
				view := domain.MarketView{Type: domain.MarketSpread, Status: "open"}
				line := strings.TrimPrefix(spread.Line, "+")
				if participant == 0 {
					view.Away, view.Home, view.Line = yesQuote, noQuote, "+"+line
				} else {
					view.Away, view.Home, view.Line = noQuote, yesQuote, "-"+line
				}
				view.Away.Outcome = event.Participants[0].Name
				view.Home.Outcome = event.Participants[1].Name
				views = append(views, view)
				selectedTickers[spread.ExchangeTicker] = true
			}
		}

		if total := closestMarket(matches, domain.MarketTotal); total != nil {
			over, under := quoteForMarket(*total, true), quoteForMarket(*total, false)
			if over != nil && under != nil {
				over.Outcome, under.Outcome = "Over", "Under"
				views = append(views, domain.MarketView{Type: domain.MarketTotal, Line: total.Line, Over: over, Under: under, Status: "open"})
				selectedTickers[total.ExchangeTicker] = true
			}
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
	return &quote
}

func closestMarket(markets []domain.CanonicalMarket, marketType domain.MarketType) *domain.CanonicalMarket {
	bestIndex, bestDistance := -1, int64(domain.Dollar)
	for i := range markets {
		market := &markets[i]
		if market.Type != marketType || market.Line == "" || market.YesAsk <= 0 || market.YesAsk >= domain.Dollar {
			continue
		}
		mid := int64(market.YesAsk)
		if market.YesBid > 0 {
			mid = (int64(market.YesBid) + int64(market.YesAsk)) / 2
		}
		distance := mid - int64(domain.Dollar/2)
		if distance < 0 {
			distance = -distance
		}
		if bestIndex < 0 || distance < bestDistance {
			bestIndex, bestDistance = i, distance
		}
	}
	if bestIndex < 0 {
		return nil
	}
	return &markets[bestIndex]
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
