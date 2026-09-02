package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
	MarketInterval      time.Duration
	ExchangeEnvironment string
	Simulated           bool
	TradingEnabled      bool
	// MaxCashRisk lowers the per-order cash-risk ceiling. Zero means the
	// engine default. It can never exceed orders.DefaultMaxCashRisk.
	MaxCashRisk domain.Money
}

type Service struct {
	mu              sync.RWMutex
	orderMu         sync.Mutex
	accountMu       sync.Mutex
	catalogMu       sync.Mutex
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
	// labels name exchange markets whose game has left the board, so History
	// and Positions stay readable. labelFailedAt throttles failed lookups.
	labels        map[string]domain.MarketLabel
	labelFailedAt map[string]time.Time
}

func New(cfg Config, store *storage.Store, adapter exchange.Adapter) *Service {
	s := &Service{cfg: cfg, store: store, schedule: schedule.Client{URL: cfg.ScheduleURL}, exchange: adapter, orderEngine: orderengine.New(cfg.TradingEnabled, adapter), books: live.NewBooks(), subscribers: make(map[chan domain.StreamEvent]struct{}), restartExchange: make(chan struct{}, 1), bookRequests: make(chan struct{}, 1), availableBooks: make(map[string]bool), labels: make(map[string]domain.MarketLabel), labelFailedAt: make(map[string]time.Time)}
	mode := "live"
	if cfg.Simulated {
		mode = "simulated"
	} else if strings.EqualFold(cfg.ExchangeEnvironment, "demo") {
		mode = "demo"
	}
	s.orderEngine.SetMaxCashRisk(cfg.MaxCashRisk)
	s.snapshot.Health = domain.Health{Status: "starting", Mode: mode, ExchangeState: "disconnected", AccountState: "pending", TradingEnabled: cfg.TradingEnabled, TradingLock: tradingLockReason(cfg, mode), MaxCashRisk: s.orderEngine.MaxCashRisk()}
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
	if labels, err := s.store.LoadMarketLabels(ctx); err != nil {
		slog.Error("load market labels", "error", err)
	} else {
		s.mu.Lock()
		s.labels = labels
		s.mu.Unlock()
	}
	if cached, err := s.store.LoadEvents(ctx); err == nil && len(cached) > 0 {
		s.setEvents(cached, false)
	}
	// Settlements load after events and labels so History is readable on the
	// first paint rather than showing raw tickers until the next reconcile.
	if settlements, err := s.store.LoadSettlements(ctx, 500); err != nil {
		slog.Error("load settlement history", "error", err)
	} else {
		s.mu.Lock()
		for i := range settlements {
			s.enrichSettlementLocked(&settlements[i])
		}
		s.snapshot.Settlements = settlements
		s.mu.Unlock()
	}
	s.refreshSchedule(ctx)
	go s.scheduleLoop(ctx)
	go s.exchangeManager(ctx)
	if !s.cfg.Simulated {
		go s.marketCatalogLoop(ctx)
	}
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
		s.seedAccountLocked()
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
	copy.AvailableToAllocate = s.availableParentCashLocked()
	return copy
}

// RefreshAccount performs the same read-only reconciliation used at startup
// and after reconnects.
func (s *Service) RefreshAccount(ctx context.Context) domain.Snapshot {
	s.reconcileAccount(ctx, true)
	return s.Snapshot()
}

func (s *Service) broadcastAccountSummary() {
	snapshot := s.Snapshot()
	s.broadcast(domain.StreamEvent{Type: "account_summary", Data: domain.AccountSummary{Bankroll: snapshot.Bankroll, AvailableToAllocate: snapshot.AvailableToAllocate, AtRisk: snapshot.AtRisk}})
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
var ErrOrderNotFound = errors.New("active order not found")
var ErrOrderNotEditable = errors.New("strategy-managed orders cannot be edited manually")
var ErrInsufficientAvailableBalance = errors.New("cash-risk target exceeds available bankroll")
var ErrMappingReviewNotFound = errors.New("mapping review not found")
var ErrInvalidMappingDecision = errors.New("mapping decision is not one of the reviewed schedule candidates")

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

type CancelAllResult struct {
	Matched  int             `json:"matched"`
	Canceled []domain.Order  `json:"canceled"`
	Failures []CancelFailure `json:"failures"`
}

type AuditPage struct {
	Records    []domain.AuditRecord `json:"records"`
	NextBefore int64                `json:"nextBefore,omitempty"`
	HasMore    bool                 `json:"hasMore"`
}

type MappingDecisionInput struct {
	EventID string `json:"eventId,omitempty"`
	Reject  bool   `json:"reject"`
}

func (s *Service) AuditHistory(ctx context.Context, beforeID int64, limit int) (AuditPage, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	records, err := s.store.LoadAudit(ctx, beforeID, limit+1)
	if err != nil {
		return AuditPage{}, err
	}
	page := AuditPage{Records: records}
	if len(records) > limit {
		page.HasMore = true
		page.Records = records[:limit]
	}
	if len(page.Records) > 0 {
		page.NextBefore = page.Records[len(page.Records)-1].ID
	}
	return page, nil
}

func (s *Service) MappingReviews(ctx context.Context, limit int) ([]domain.MappingReview, error) {
	return s.store.LoadMappingReviews(ctx, limit)
}

func (s *Service) DecideMappingReview(ctx context.Context, id string, input MappingDecisionInput) (domain.MappingReview, error) {
	review, ok, err := s.store.MappingReview(ctx, id)
	if err != nil {
		return domain.MappingReview{}, err
	}
	if !ok {
		return domain.MappingReview{}, ErrMappingReviewNotFound
	}
	eventID, status := strings.TrimSpace(input.EventID), "manual_accepted"
	if input.Reject {
		eventID, status = "", "manual_rejected"
	} else {
		valid := false
		for _, candidate := range review.Candidates {
			if candidate.EventID == eventID {
				valid = true
				break
			}
		}
		if !valid {
			return review, ErrInvalidMappingDecision
		}
	}
	overrides := make([]domain.MappingOverride, 0, len(review.Tickers))
	for _, ticker := range review.Tickers {
		overrides = append(overrides, domain.MappingOverride{Exchange: review.Exchange, Ticker: ticker, EventID: eventID, Status: status})
	}
	if err := s.store.SaveMappingOverrides(ctx, overrides); err != nil {
		return review, err
	}
	if err := s.store.DeleteMappingReview(ctx, id); err != nil {
		return review, err
	}
	_ = s.store.Audit(ctx, "mapping_review_decided", map[string]any{"review": review, "event_id": eventID, "status": status})
	select {
	case s.restartExchange <- struct{}{}:
	default:
	}
	return review, nil
}

func (s *Service) CreateParentOrder(ctx context.Context, input CreateParentOrderInput) (domain.ParentOrder, error) {
	// Serialize parent creation so two browser requests cannot both spend the
	// same available balance before either exchange acknowledgement is applied.
	s.orderMu.Lock()
	defer s.orderMu.Unlock()
	s.mu.RLock()
	tradingEnabled := s.snapshot.Health.TradingEnabled
	available := s.availableBooks[input.Ticker]
	event, quote, marketLabel, rotation, found := s.resolveOrderSelection(input.EventID, input.Ticker, input.Side)
	availableCash := s.availableParentCashLocked()
	s.mu.RUnlock()
	if !tradingEnabled {
		return domain.ParentOrder{}, orderengine.ErrDisabled
	}
	if !available || !found {
		return domain.ParentOrder{}, orderengine.ErrInvalidOrder
	}
	if input.CashRisk <= 0 || input.CashRisk > availableCash {
		return domain.ParentOrder{}, ErrInsufficientAvailableBalance
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
	s.enrichOrderLocked(&child)
	s.upsertOrderLocked(child)
	s.snapshot.Bankroll -= parent.ReservedRisk
	if s.snapshot.Bankroll < 0 {
		s.snapshot.Bankroll = 0
	}
	s.recalculateParentRiskLocked()
	s.mu.Unlock()
	if err := s.store.SaveParentOrder(ctx, parent); err != nil {
		slog.Error("persist acknowledged parent order", "parent_id", parent.ID, "error", err)
	}
	_ = s.store.Audit(ctx, "parent_order_acknowledged", map[string]any{"parent": parent, "child": child})
	s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
	s.broadcast(domain.StreamEvent{Type: "order", Data: child})
	s.broadcastAccountSummary()
	if parent.Strategy == "follow" {
		s.queueBookRefresh()
	}
	return parent, nil
}

func (s *Service) availableParentCashLocked() domain.Money {
	available := s.snapshot.Bankroll
	for _, parent := range s.snapshot.ParentOrders {
		if parentOrderTerminal(parent.Status) {
			continue
		}
		unreserved := parent.RemainingRisk - parent.ReservedRisk
		if unreserved > 0 {
			available -= unreserved
		}
	}
	if available < 0 {
		return 0
	}
	return available
}

func tradingLockReason(cfg Config, mode string) string {
	if cfg.TradingEnabled {
		return ""
	}
	if strings.EqualFold(cfg.ExchangeEnvironment, "production") {
		return "Production order entry is off until enabled on the server."
	}
	if mode == "simulated" {
		return "Simulated mode is read-only."
	}
	return "Demo order entry is off until explicitly enabled on the server."
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
	s.broadcastAccountSummary()
	if parent.Strategy == "follow" {
		s.queueBookRefresh()
	}
	return parent, nil
}

// CancelOrder cancels both PMBattle-managed children and live orders recovered
// from Kalshi account reconciliation. Recovered orders have no parent record,
// but must still be cancellable from the trading station.
func (s *Service) CancelOrder(ctx context.Context, id string) (domain.Order, error) {
	if !s.Snapshot().Health.TradingEnabled {
		return domain.Order{}, orderengine.ErrDisabled
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return domain.Order{}, ErrOrderNotFound
	}
	if parent, ok := s.orderEngine.ParentForChild(id); ok {
		if _, err := s.CancelParentOrder(ctx, parent.ID); err != nil {
			return domain.Order{}, err
		}
		for _, order := range s.Snapshot().Orders {
			if order.ID == id {
				return order, nil
			}
		}
		return domain.Order{ID: id, Exchange: "Kalshi", Status: "canceled"}, nil
	}

	s.orderMu.Lock()
	defer s.orderMu.Unlock()
	s.mu.RLock()
	var target domain.Order
	for _, order := range s.snapshot.Orders {
		if order.ID == id && !parentOrderTerminal(order.Status) {
			target = order
			break
		}
	}
	s.mu.RUnlock()
	if target.ID == "" {
		return domain.Order{}, ErrOrderNotFound
	}
	_ = s.store.Audit(ctx, "order_cancel_requested", target)
	if err := s.exchange.CancelOrder(ctx, id); err != nil {
		_ = s.store.Audit(ctx, "order_cancel_failed", map[string]any{"order": target, "error": err.Error()})
		return domain.Order{}, err
	}
	target.Status = "canceled"
	target.CashRisk = 0
	s.mu.Lock()
	for i := range s.snapshot.Orders {
		if s.snapshot.Orders[i].ID == id {
			s.snapshot.Orders[i] = target
			break
		}
	}
	s.recalculateParentRiskLocked()
	s.mu.Unlock()
	_ = s.store.Audit(ctx, "order_canceled", target)
	s.broadcast(domain.StreamEvent{Type: "order", Data: target})
	s.broadcastAccountSummary()
	return target, nil
}

func (s *Service) AmendOrder(ctx context.Context, id string, remainingQuantity, limitPrice domain.Money) (domain.Order, error) {
	if !s.Snapshot().Health.TradingEnabled {
		return domain.Order{}, orderengine.ErrDisabled
	}
	if remainingQuantity <= 0 || limitPrice <= 0 || limitPrice >= domain.Dollar {
		return domain.Order{}, orderengine.ErrInvalidOrder
	}
	s.orderMu.Lock()
	defer s.orderMu.Unlock()
	s.mu.RLock()
	var target domain.Order
	for _, order := range s.snapshot.Orders {
		if order.ID == id && !parentOrderTerminal(order.Status) {
			target = order
			break
		}
	}
	availableCash := s.availableParentCashLocked()
	s.mu.RUnlock()
	if target.ID == "" {
		return domain.Order{}, ErrOrderNotFound
	}
	if parent, ok := s.orderEngine.ParentForChild(id); ok && parent.Strategy != "basic" {
		return domain.Order{}, ErrOrderNotEditable
	}
	quote, err := pricing.Quote(limitPrice, remainingQuantity, false)
	if err != nil {
		return domain.Order{}, orderengine.ErrInvalidOrder
	}
	if quote.AllInCost > s.orderEngine.MaxCashRisk() {
		return domain.Order{}, orderengine.ErrCashRiskCap
	}
	// Replacing the current order releases its existing reservation, so the
	// amended order may use available cash plus that reservation, but no more.
	if quote.AllInCost > availableCash+target.CashRisk {
		_ = s.store.Audit(ctx, "order_amend_rejected", map[string]any{"order": target, "remaining_quantity": remainingQuantity, "limit_price": limitPrice, "error": ErrInsufficientAvailableBalance.Error()})
		return domain.Order{}, ErrInsufficientAvailableBalance
	}
	request := exchange.AmendOrderRequest{OrderID: id, Ticker: target.Ticker, OutcomeSide: target.Side, Quantity: target.FilledQuantity + remainingQuantity, LimitPrice: limitPrice}
	_ = s.store.Audit(ctx, "order_amend_requested", map[string]any{"order": target, "remaining_quantity": remainingQuantity, "limit_price": limitPrice})
	amended, err := s.exchange.AmendOrder(ctx, request)
	if err != nil {
		_ = s.store.Audit(ctx, "order_amend_failed", map[string]any{"order": target, "error": err.Error()})
		return domain.Order{}, err
	}
	if amended.ID == "" {
		amended.ID = id
	}
	amended.Exchange, amended.Ticker, amended.Side = target.Exchange, target.Ticker, target.Side
	amended.Rotation, amended.Game, amended.Outcome, amended.Market = target.Rotation, target.Game, target.Outcome, target.Market
	amended.Quantity, amended.FilledQuantity, amended.LimitPrice, amended.CashRisk = request.Quantity, target.FilledQuantity, limitPrice, quote.AllInCost
	if amended.Status == "" {
		amended.Status = "resting"
	}
	if amended.CreatedAt.IsZero() {
		amended.CreatedAt = target.CreatedAt
	}
	parent, managed := s.orderEngine.RecordManualAmend(id, amended, quote.AllInCost)
	s.mu.Lock()
	filtered := s.snapshot.Orders[:0]
	for _, order := range s.snapshot.Orders {
		if order.ID != id && order.ID != amended.ID {
			filtered = append(filtered, order)
		}
	}
	s.snapshot.Orders = append([]domain.Order{amended}, filtered...)
	s.snapshot.Bankroll -= quote.AllInCost - target.CashRisk
	if s.snapshot.Bankroll < 0 {
		s.snapshot.Bankroll = 0
	}
	if managed {
		s.upsertParentLocked(parent)
	}
	s.recalculateParentRiskLocked()
	s.mu.Unlock()
	if managed {
		_ = s.store.SaveParentOrder(ctx, parent)
		s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
	}
	_ = s.store.Audit(ctx, "order_amended", amended)
	s.broadcast(domain.StreamEvent{Type: "order", Data: amended})
	s.broadcastAccountSummary()
	return amended, nil
}

// CancelAllOrders covers the actual exchange account, including orders that
// were created outside PMBattle or recovered after a restart.
func (s *Service) CancelAllOrders(ctx context.Context) (CancelAllResult, error) {
	if !s.Snapshot().Health.TradingEnabled {
		return CancelAllResult{}, orderengine.ErrDisabled
	}
	targets := make([]domain.Order, 0)
	for _, order := range s.Snapshot().Orders {
		if !parentOrderTerminal(order.Status) {
			targets = append(targets, order)
		}
	}
	result := CancelAllResult{Matched: len(targets), Canceled: []domain.Order{}, Failures: []CancelFailure{}}
	_ = s.store.Audit(ctx, "all_orders_cancel_requested", map[string]any{"matched": len(targets)})
	for _, target := range targets {
		order, err := s.CancelOrder(ctx, target.ID)
		if errors.Is(err, ErrOrderNotFound) {
			continue // Another child from the same managed parent canceled it.
		}
		if err != nil {
			result.Failures = append(result.Failures, CancelFailure{ParentID: target.ID, Error: err.Error()})
			continue
		}
		result.Canceled = append(result.Canceled, order)
	}
	_ = s.store.Audit(ctx, "all_orders_cancel_result", result)
	return result, nil
}

func (s *Service) ResumeParentOrder(ctx context.Context, id string) (domain.ParentOrder, error) {
	if !s.Snapshot().Health.TradingEnabled {
		return domain.ParentOrder{}, orderengine.ErrDisabled
	}
	existing, ok := s.orderEngine.Parent(id)
	if !ok {
		return domain.ParentOrder{}, orderengine.ErrNotFound
	}
	book, ok := s.books.Get(existing.Ticker)
	if !ok || book.Stale {
		return existing, errors.New("order book is not synchronized")
	}
	resumed, err := s.orderEngine.ResumeFollow(id)
	if err != nil {
		return resumed, err
	}
	_ = s.store.Audit(ctx, "follow_resume_requested", map[string]any{"parent": resumed, "book_sequence": book.Sequence})
	results := s.orderEngine.HandleBook(ctx, book)
	final := resumed
	published := false
	var strategyErr error
	for _, result := range results {
		if result.Parent.ID == id {
			final = result.Parent
			strategyErr = result.Err
			published = result.Changed
		}
	}
	s.publishFollowResults(results, book.Sequence)
	if !published {
		s.mu.Lock()
		s.upsertParentLocked(final)
		s.recalculateParentRiskLocked()
		s.mu.Unlock()
		if err := s.store.SaveParentOrder(ctx, final); err != nil {
			slog.Error("persist resumed follow", "parent_id", final.ID, "error", err)
		}
		s.broadcast(domain.StreamEvent{Type: "parent_order", Data: final})
	}
	_ = s.store.Audit(ctx, "follow_resume_result", map[string]any{"parent": final, "strategy_error": errorText(strategyErr)})
	if strategyErr != nil {
		return final, strategyErr
	}
	return final, nil
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
	accountRisk := domain.Money(0)
	for _, position := range s.snapshot.Positions {
		accountRisk += absMoney(position.CashRisk)
	}
	for _, order := range s.snapshot.Orders {
		if !terminalOrderStatus(order.Status) {
			accountRisk += absMoney(order.CashRisk)
		}
	}
	managedRisk := domain.Money(0)
	for _, parent := range s.snapshot.ParentOrders {
		managedRisk += parent.FilledRisk
		if parent.Status != "canceled" && parent.Status != "filled" && parent.Status != "rejected" {
			managedRisk += parent.ReservedRisk
		}
	}
	s.snapshot.AtRisk = accountRisk
	if managedRisk > accountRisk {
		s.snapshot.AtRisk = managedRisk
	}
}

func terminalOrderStatus(status string) bool {
	switch strings.ToLower(status) {
	case "canceled", "cancelled", "executed", "filled", "closed", "rejected":
		return true
	default:
		return false
	}
}

func absMoney(value domain.Money) domain.Money {
	if value < 0 {
		return -value
	}
	return value
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

func (s *Service) marketCatalogLoop(ctx context.Context) {
	interval := s.cfg.MarketInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshExchangeMarkets(ctx, true)
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
		s.seedAccountLocked()
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
	// Balance and positions first: they are quick and the browser shows them
	// immediately. Market discovery can take a while and a restart (for
	// example after saving preferences) must not leave the account panel
	// waiting on it.
	s.reconcileAccount(ctx, false)
	s.refreshExchangeMarkets(ctx, false)
	accountTicker := time.NewTicker(30 * time.Second)
	defer accountTicker.Stop()
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
			case <-accountTicker.C:
				s.reconcileAccount(ctx, true)
			}
		}
		_ = subscription.Close()
		s.reconcileAccount(ctx, true)
		s.setExchangeHealth("reconnecting", 0)
	}
}

func (s *Service) refreshExchangeMarkets(ctx context.Context, publish bool) {
	if s.exchange == nil {
		return
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	s.mu.RLock()
	events := append([]domain.CanonicalEvent(nil), s.snapshot.Events...)
	s.mu.RUnlock()
	markets, err := s.exchange.ListMarkets(ctx, events)
	if err != nil {
		slog.Warn("market discovery failed", "error", err)
		return
	}
	slog.Info("market discovery complete", "schedule_events", len(events), "exchange_markets", len(markets))
	s.rememberLabels(ctx, markets)
	matched := mapping.Match(events, markets)
	overrides, overrideErr := s.store.LoadMappingOverrides(ctx, s.exchange.Name())
	if overrideErr != nil {
		slog.Warn("load manual mapping overrides", "error", overrideErr)
	} else {
		applyMappingOverrides(matched, overrides)
	}
	reviews := buildMappingReviews(events, matched, s.exchange.Name())
	if err := s.store.ReplaceMappingReviews(ctx, s.exchange.Name(), reviews); err != nil {
		slog.Warn("persist mapping review queue", "error", err)
	}
	accepted := 0
	for _, market := range matched {
		_ = s.store.SaveMapping(ctx, market)
		if market.MappingStatus == "accepted" {
			accepted++
		}
	}
	tickers := make([]string, 0)
	if !s.cfg.Simulated {
		tickers = s.attachMatched(matched)
	}
	s.setAvailableBooks(tickers)
	slog.Info("market mapping complete", "accepted_markets", accepted, "available_books", len(tickers), "review_groups", len(reviews))
	if publish {
		s.broadcast(domain.StreamEvent{Type: "schedule", Data: s.Snapshot().Events})
	}
}

func applyMappingOverrides(markets []domain.CanonicalMarket, overrides map[string]domain.MappingOverride) {
	for i := range markets {
		override, ok := overrides[markets[i].ExchangeTicker]
		if !ok {
			continue
		}
		switch override.Status {
		case "manual_accepted":
			markets[i].EventID = override.EventID
			markets[i].MappingConfidence = 100
			markets[i].MappingStatus = "accepted"
		case "manual_rejected":
			markets[i].EventID = ""
			markets[i].MappingStatus = "rejected"
		}
	}
}

func buildMappingReviews(events []domain.CanonicalEvent, markets []domain.CanonicalMarket, fallbackExchange string) []domain.MappingReview {
	type reviewGroup struct {
		review  domain.MappingReview
		market  domain.CanonicalMarket
		types   map[domain.MarketType]bool
		tickers map[string]bool
	}
	groups := make(map[string]*reviewGroup)
	for _, market := range markets {
		if market.MappingStatus != "review" {
			continue
		}
		exchangeName := strings.ToLower(strings.TrimSpace(market.Exchange))
		if exchangeName == "" {
			exchangeName = strings.ToLower(fallbackExchange)
		}
		key := exchangeName + "\x00" + strings.TrimSpace(market.Title) + "\x00" + market.OccurrenceTime.UTC().Format(time.RFC3339Nano)
		group := groups[key]
		if group == nil {
			sum := sha256.Sum256([]byte(key))
			group = &reviewGroup{review: domain.MappingReview{ID: fmt.Sprintf("%x", sum[:12]), Exchange: exchangeName, Title: market.Title, OccurrenceTime: market.OccurrenceTime}, market: market, types: make(map[domain.MarketType]bool), tickers: make(map[string]bool)}
			groups[key] = group
		}
		group.types[market.Type] = true
		if market.ExchangeTicker != "" {
			group.tickers[market.ExchangeTicker] = true
		}
	}
	reviews := make([]domain.MappingReview, 0, len(groups))
	for _, group := range groups {
		group.review.Candidates = mapping.Candidates(events, group.market)
		// Completely unrelated contracts stay hidden, but do not flood the
		// human queue without any evidence-based schedule choice.
		if len(group.review.Candidates) == 0 {
			continue
		}
		for ticker := range group.tickers {
			group.review.Tickers = append(group.review.Tickers, ticker)
		}
		for marketType := range group.types {
			group.review.MarketTypes = append(group.review.MarketTypes, marketType)
		}
		sort.Strings(group.review.Tickers)
		sort.Slice(group.review.MarketTypes, func(i, j int) bool { return group.review.MarketTypes[i] < group.review.MarketTypes[j] })
		reviews = append(reviews, group.review)
	}
	sort.SliceStable(reviews, func(i, j int) bool {
		if reviews[i].OccurrenceTime.Equal(reviews[j].OccurrenceTime) {
			return reviews[i].Title < reviews[j].Title
		}
		return reviews[i].OccurrenceTime.Before(reviews[j].OccurrenceTime)
	})
	return reviews
}

func (s *Service) reconcileAccount(ctx context.Context, publish bool) {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	previousState := s.snapshot.Health.AccountState
	s.snapshot.Health.AccountState = "syncing"
	s.mu.Unlock()
	// A reconcile interrupted by shutdown or an exchange-loop restart must
	// not publish half-finished state or mark the account degraded; the
	// replacement loop reconciles again immediately.
	interrupted := func() bool {
		if ctx.Err() == nil {
			return false
		}
		s.mu.Lock()
		s.snapshot.Health.AccountState = previousState
		s.mu.Unlock()
		return true
	}
	accountOK := true
	if balance, balanceErr := s.exchange.Balance(ctx); balanceErr != nil {
		if interrupted() {
			return
		}
		slog.Warn("balance reconciliation failed", "error", balanceErr)
		accountOK = false
	} else if interrupted() {
		return
	} else {
		s.mu.Lock()
		s.snapshot.Bankroll = balance
		s.mu.Unlock()
	}
	orders, positions, fills, err := s.exchange.Snapshot(ctx)
	if interrupted() {
		return
	}
	if err != nil {
		slog.Warn("account reconciliation failed", "error", err)
		accountOK = false
	} else {
		s.lookupMissingLabels(ctx, accountTickers(orders, positions, nil, fills))
		if interrupted() {
			return
		}
		reconciledParents := make([]domain.ParentOrder, 0)
		for _, order := range orders {
			if parent, ok := s.orderEngine.ApplyOrder(order); ok {
				reconciledParents = append(reconciledParents, parent)
			}
		}
		s.mu.Lock()
		for i := range positions {
			s.enrichPositionLocked(&positions[i])
		}
		for i := range orders {
			s.enrichOrderLocked(&orders[i])
		}
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
		s.mergeHistoricalFills(fills)
	}
	s.reconcileSettlements(ctx)
	s.reconcileParentFills(ctx, publish)
	s.mu.Lock()
	if accountOK {
		s.snapshot.Health.AccountState = "ready"
		s.snapshot.Health.AccountUpdated = time.Now().UTC()
	} else {
		s.snapshot.Health.AccountState = "degraded"
	}
	s.mu.Unlock()
	snapshot := s.Snapshot()
	s.broadcast(domain.StreamEvent{Type: "health", Data: snapshot.Health})
	s.broadcast(domain.StreamEvent{Type: "account_snapshot", Data: domain.AccountSnapshot{ParentOrders: snapshot.ParentOrders, Orders: snapshot.Orders, Positions: snapshot.Positions, Settlements: snapshot.Settlements, Fills: snapshot.Fills, Bankroll: snapshot.Bankroll, AvailableToAllocate: snapshot.AvailableToAllocate, AtRisk: snapshot.AtRisk}})
}

func (s *Service) reconcileSettlements(ctx context.Context) {
	since, err := s.store.LatestSettlementTime(ctx, s.exchange.Name())
	if err != nil {
		slog.Warn("settlement cursor load failed", "error", err)
		return
	}
	settlements, err := s.exchange.Settlements(ctx, since)
	if err != nil {
		slog.Warn("settlement reconciliation failed", "error", err)
		return
	}
	if err := s.store.SaveSettlements(ctx, settlements); err != nil {
		slog.Warn("settlement persistence failed", "error", err)
		return
	}
	history, err := s.store.LoadSettlements(ctx, 500)
	if err != nil {
		slog.Warn("settlement history load failed", "error", err)
		return
	}
	// Name the whole history, so rows stored before names existed become
	// readable too.
	s.lookupMissingLabels(ctx, accountTickers(nil, nil, history, nil))
	s.mu.Lock()
	for i := range history {
		s.enrichSettlementLocked(&history[i])
	}
	s.snapshot.Settlements = history
	s.mu.Unlock()
}

func (s *Service) enrichPositionLocked(position *domain.Position) {
	d := s.describeLocked(position.Ticker, position.Side)
	position.Game, position.Outcome, position.Rotation = d.Game, d.Outcome, d.Rotation
	if d.EventID != "" {
		position.EventID = d.EventID
	}
	position.Market = d.Market
	if position.Market == "" {
		position.Market = position.Ticker
	}
}

// enrichSettlementLocked names a settled market. The settled side is unknown
// from the payload alone, so the yes/no counts decide which outcome to show.
func (s *Service) enrichSettlementLocked(settlement *domain.Settlement) {
	side := "yes"
	if settlement.NoQuantity > settlement.YesQuantity {
		side = "no"
	}
	d := s.describeLocked(settlement.Ticker, side)
	settlement.Game, settlement.Outcome, settlement.Rotation = d.Game, d.Outcome, d.Rotation
	if d.EventID != "" {
		settlement.EventID = d.EventID
	}
	settlement.Market = d.Market
	if settlement.Market == "" {
		settlement.Market = settlement.Ticker
	}
}

func marketName(market domain.MarketView) string {
	name := map[domain.MarketType]string{domain.MarketMoneyline: "Moneyline", domain.MarketSpread: "Spread", domain.MarketTotal: "Total"}[market.Type]
	if name == "" {
		name = string(market.Type)
	}
	if market.Line != "" {
		name += " " + market.Line
	}
	return name
}

func rotationForOutcome(event domain.CanonicalEvent, outcome string) string {
	for _, participant := range event.Participants {
		if strings.EqualFold(participant.Name, outcome) {
			return participant.Rotation
		}
	}
	return ""
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
	s.snapshot.Health.MappedMarkets = len(available)
	active := s.activeBook
	if active != "" && !available[active] {
		s.activeBook = ""
	}
	health := s.snapshot.Health
	s.mu.Unlock()
	s.broadcast(domain.StreamEvent{Type: "health", Data: health})
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
			s.enrichOrderLocked(&order)
			s.upsertOrderLocked(order)
			if parentMatched {
				s.upsertParentLocked(parent)
			}
			s.recalculateParentRiskLocked()
			s.mu.Unlock()
			if parentMatched {
				if err := s.store.SaveParentOrder(context.Background(), parent); err != nil {
					slog.Error("persist order-reconciled parent order", "parent_id", parent.ID, "order_id", order.ID, "error", err)
				}
				s.broadcast(domain.StreamEvent{Type: "parent_order", Data: parent})
				s.queueBookRefresh()
			}
			s.broadcast(event)
			s.broadcastAccountSummary()
		}
	case "position":
		if position, ok := event.Data.(domain.Position); ok {
			s.mu.Lock()
			s.enrichPositionLocked(&position)
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
			s.recalculateParentRiskLocked()
			s.mu.Unlock()
			s.broadcast(event)
			s.broadcastAccountSummary()
		}
	default:
		s.broadcast(event)
	}
}

func (s *Service) applyFollowBook(book domain.OrderBook) {
	s.publishFollowResults(s.orderEngine.HandleBook(context.Background(), book), book.Sequence)
}

func (s *Service) publishFollowResults(results []orderengine.FollowResult, bookSequence int64) {
	for _, result := range results {
		if !result.Changed || result.Parent.ID == "" {
			continue
		}
		s.mu.Lock()
		s.upsertParentLocked(result.Parent)
		if result.Order != nil {
			s.enrichOrderLocked(result.Order)
			s.upsertOrderLocked(*result.Order)
		}
		s.recalculateParentRiskLocked()
		s.mu.Unlock()
		if err := s.store.SaveParentOrder(context.Background(), result.Parent); err != nil {
			slog.Error("persist follow decision", "parent_id", result.Parent.ID, "error", err)
		}
		audit := map[string]any{"parent": result.Parent, "book_sequence": bookSequence}
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
		s.broadcastAccountSummary()
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
	s.mu.RLock()
	s.enrichFillLocked(&fill)
	s.mu.RUnlock()
	if reconciled {
		s.mu.Lock()
		if refreshedChild != nil {
			refreshedChild.Rotation = parent.Rotation
			s.enrichOrderLocked(refreshedChild)
		}
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
		s.broadcastAccountSummary()
		s.broadcast(domain.StreamEvent{Type: "fill", Data: fill})
	}
	return true
}

// mergeHistoricalFills restores the account tray without replaying old fills
// through strategy, notification, and audit paths one record at a time.
// Managed parents have a separate order-scoped recovery pass immediately after
// account reconciliation, so missed strategy fills are still handled safely.
func (s *Service) mergeHistoricalFills(fills []domain.Fill) {
	if len(fills) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]bool, len(s.snapshot.Fills)+len(fills))
	merged := make([]domain.Fill, 0, len(s.snapshot.Fills)+len(fills))
	for _, fill := range s.snapshot.Fills {
		key := fill.ID
		if key == "" {
			key = fill.OrderID + "|" + fill.CreatedAt.Format(time.RFC3339Nano) + "|" + strconv.FormatInt(int64(fill.Quantity), 10)
		}
		if !seen[key] {
			seen[key] = true
			merged = append(merged, fill)
		}
	}
	for _, fill := range fills {
		key := fill.ID
		if key == "" {
			key = fill.OrderID + "|" + fill.CreatedAt.Format(time.RFC3339Nano) + "|" + strconv.FormatInt(int64(fill.Quantity), 10)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		s.enrichFillLocked(&fill)
		merged = append(merged, fill)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].CreatedAt.After(merged[j].CreatedAt) })
	if len(merged) > 250 {
		merged = merged[:250]
	}
	s.snapshot.Fills = merged
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
		event := &s.snapshot.Events[i]
		views := make([]domain.MarketView, 0, 3)

		moneyline := domain.MarketView{Type: domain.MarketMoneyline, Status: "open"}
		for _, market := range matches {
			if market.Type != domain.MarketMoneyline {
				continue
			}
			participant := mapping.ParticipantIndex(*event, market.Outcome)
			if participant < 0 {
				participant = mapping.ParticipantIndex(*event, market.Subtitle)
			}
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
	// Keep the unfiltered schedule in sync too. Preferences are applied from
	// allEvents, so a settings change must not erase already verified markets.
	viewsByEvent := make(map[string][]domain.MarketView, len(s.snapshot.Events))
	for _, event := range s.snapshot.Events {
		viewsByEvent[event.ID] = event.Markets
	}
	for i := range s.allEvents {
		if views, ok := viewsByEvent[s.allEvents[i].ID]; ok {
			s.allEvents[i].Markets = views
		}
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
	participant := mapping.ParticipantIndex(event, market.Outcome)
	if participant < 0 {
		participant = mapping.ParticipantIndex(event, market.Subtitle)
	}
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
	health := s.snapshot.Health
	s.mu.Unlock()
	s.broadcast(domain.StreamEvent{Type: "health", Data: health})
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

// seedAccountLocked fills the simulated demo account and names its rows
// through the same path live rows use. Callers hold s.mu.
func (s *Service) seedAccountLocked() {
	seedAccount(&s.snapshot)
	for i := range s.snapshot.Orders {
		s.enrichOrderLocked(&s.snapshot.Orders[i])
	}
	for i := range s.snapshot.Positions {
		s.enrichPositionLocked(&s.snapshot.Positions[i])
	}
	for i := range s.snapshot.Fills {
		s.enrichFillLocked(&s.snapshot.Fills[i])
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
	ticker := "SIM-" + event.ID + "-ML-H"
	snapshot.Orders = []domain.Order{{ID: "sim-order-1", Exchange: "Kalshi", Ticker: ticker, Rotation: event.Participants[1].Rotation, Market: "Moneyline", Side: "yes", Status: "resting", Quantity: 2500 * domain.Dollar, LimitPrice: 5800, CashRisk: 1450 * domain.Dollar, CreatedAt: now.Add(-3 * time.Minute)}}
	snapshot.Fills = []domain.Fill{{ID: "sim-fill-1", Exchange: "Kalshi", Ticker: ticker, EventID: event.ID, Rotation: event.Participants[1].Rotation, Team: event.Participants[1].Name, Market: "Moneyline", Side: "yes", Quantity: 1000 * domain.Dollar, RawPrice: 5700, AllInMoneyline: -138, Fee: 28 * domain.Dollar, CashRisk: 598 * domain.Dollar, CreatedAt: now.Add(-time.Minute)}}
	snapshot.Positions = []domain.Position{{Exchange: "Kalshi", Ticker: ticker, Rotation: event.Participants[1].Rotation, Market: "Moneyline", Side: "yes", Quantity: 1000 * domain.Dollar, CashRisk: 598 * domain.Dollar, AveragePrice: 5700, CurrentPrice: 5800, UnrealizedPnL: 10 * domain.Dollar}}
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
