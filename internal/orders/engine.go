package orders

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
)

// DefaultMaxCashRisk is the hard ceiling for one parent order. Operators can
// lower it with PMBATTLE_MAX_CASH_RISK but never raise it above this value.
const DefaultMaxCashRisk = 20_000 * domain.Dollar

const FollowRepriceInterval = 750 * time.Millisecond

var (
	ErrDisabled            = errors.New("demo trading is disabled")
	ErrUnsupportedStrategy = errors.New("strategy is not available in this demo release")
	ErrInvalidOrder        = errors.New("invalid parent order")
	ErrCashRiskCap         = errors.New("cash risk exceeds the per-order cap set on this server")
	ErrPriceCap            = errors.New("fee-adjusted price exceeds the parent order cap")
	ErrNotFound            = errors.New("parent order not found")
	ErrNotResumable        = errors.New("parent order is not a paused follow order with an active child")
)

type Executor interface {
	PlaceOrder(context.Context, exchange.PlaceOrderRequest) (domain.Order, error)
	AmendOrder(context.Context, exchange.AmendOrderRequest) (domain.Order, error)
	CancelOrder(context.Context, string) error
}

type FollowResult struct {
	Parent  domain.ParentOrder
	Order   *domain.Order
	Changed bool
	Err     error
}

type CreateRequest struct {
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

type Engine struct {
	mu          sync.RWMutex
	enabled     bool
	exec        Executor
	maxCashRisk domain.Money
	parents     map[string]domain.ParentOrder
	now         func() time.Time
}

func New(enabled bool, executor Executor) *Engine {
	return &Engine{enabled: enabled, exec: executor, maxCashRisk: DefaultMaxCashRisk, parents: make(map[string]domain.ParentOrder), now: func() time.Time { return time.Now().UTC() }}
}

// SetMaxCashRisk lowers the per-order cash-risk ceiling. Values at or below
// zero or above DefaultMaxCashRisk are clamped to the default.
func (e *Engine) SetMaxCashRisk(limit domain.Money) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if limit <= 0 || limit > DefaultMaxCashRisk {
		limit = DefaultMaxCashRisk
	}
	e.maxCashRisk = limit
}

func (e *Engine) MaxCashRisk() domain.Money {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.maxCashRisk
}

func (e *Engine) Restore(parents []domain.ParentOrder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, parent := range parents {
		if parent.ID == "" {
			continue
		}
		if parent.RemainingRisk == 0 && parent.FilledRisk < parent.CashRiskTarget && !terminal(parent.Status) {
			parent.RemainingRisk = parent.CashRiskTarget - parent.FilledRisk
		}
		e.parents[parent.ID] = cloneParent(parent)
	}
}

func (e *Engine) List() []domain.ParentOrder {
	e.mu.RLock()
	defer e.mu.RUnlock()
	parents := make([]domain.ParentOrder, 0, len(e.parents))
	for _, parent := range e.parents {
		parents = append(parents, cloneParent(parent))
	}
	return parents
}

func (e *Engine) ParentForChild(childOrderID string) (domain.ParentOrder, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, parent := range e.parents {
		if contains(parent.ChildOrderIDs, childOrderID) {
			return cloneParent(parent), true
		}
	}
	return domain.ParentOrder{}, false
}

func (e *Engine) Parent(parentID string) (domain.ParentOrder, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	parent, ok := e.parents[parentID]
	return cloneParent(parent), ok
}

func (e *Engine) Create(ctx context.Context, request CreateRequest) (domain.ParentOrder, domain.Order, error) {
	if !e.enabled || e.exec == nil {
		return domain.ParentOrder{}, domain.Order{}, ErrDisabled
	}
	request.Strategy = strings.ToLower(strings.TrimSpace(request.Strategy))
	request.Policy = strings.ToLower(strings.TrimSpace(request.Policy))
	request.Side = strings.ToLower(strings.TrimSpace(request.Side))
	if request.Strategy != "basic" && request.Strategy != "iceberg" && request.Strategy != "follow" {
		return domain.ParentOrder{}, domain.Order{}, ErrUnsupportedStrategy
	}
	if request.Ticker == "" || request.EventID == "" || request.Outcome == "" || request.Market == "" || request.Side != "yes" && request.Side != "no" || request.LimitPrice <= 0 || request.LimitPrice >= domain.Dollar || request.CashRisk < domain.Dollar {
		return domain.ParentOrder{}, domain.Order{}, ErrInvalidOrder
	}
	if request.CashRisk > e.MaxCashRisk() {
		return domain.ParentOrder{}, domain.Order{}, ErrCashRiskCap
	}
	timeInForce, postOnly, ok := policy(request.Policy)
	if !ok || (request.Strategy == "iceberg" || request.Strategy == "follow") && request.Policy == "ioc" {
		return domain.ParentOrder{}, domain.Order{}, ErrInvalidOrder
	}
	if request.Strategy == "follow" {
		timeInForce = "good_till_canceled"
		postOnly = true
		request.Policy = "post_only"
	}
	quantity, quote, err := QuantityForCashRisk(request.LimitPrice, request.CashRisk)
	if err != nil || quantity <= 0 {
		return domain.ParentOrder{}, domain.Order{}, ErrInvalidOrder
	}
	if !withinCap(quote.AllInMoneyline, request.PriceCapMoneyline) {
		return domain.ParentOrder{}, domain.Order{}, ErrPriceCap
	}
	if request.Strategy == "iceberg" && request.SliceQuantity <= 0 {
		return domain.ParentOrder{}, domain.Order{}, ErrInvalidOrder
	}
	id, err := newID()
	if err != nil {
		return domain.ParentOrder{}, domain.Order{}, err
	}
	now := e.now()
	parent := domain.ParentOrder{
		ID: id, Exchange: "Kalshi", EventID: request.EventID, Ticker: request.Ticker,
		Rotation: request.Rotation, Outcome: request.Outcome, Market: request.Market, Side: request.Side,
		Strategy: request.Strategy, Policy: request.Policy, Status: "submitting", CashRiskTarget: request.CashRisk,
		ReservedRisk: quote.AllInCost, RemainingRisk: request.CashRisk, PriceCapMoneyline: request.PriceCapMoneyline,
		LimitPrice: request.LimitPrice, Quantity: quantity, SliceQuantity: request.SliceQuantity, CreatedAt: now, UpdatedAt: now,
	}
	childQuantity := quantity
	if request.Strategy == "iceberg" && request.SliceQuantity < childQuantity {
		childQuantity = request.SliceQuantity
	}
	child, err := e.exec.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Ticker: request.Ticker, OutcomeSide: request.Side, Quantity: childQuantity, LimitPrice: request.LimitPrice,
		TimeInForce: timeInForce, PostOnly: postOnly, ClientOrderID: id,
	})
	if err != nil {
		return parent, domain.Order{}, err
	}
	parent.Status = child.Status
	if parent.Status == "" {
		parent.Status = "submitted"
	}
	parent.ChildOrderIDs = []string{child.ID}
	parent.Children = []domain.ChildOrderState{childState(child, id)}
	parent.UpdatedAt = time.Now().UTC()
	e.mu.Lock()
	e.parents[parent.ID] = parent
	e.mu.Unlock()
	return parent, child, nil
}

// HandleBook advances all follow parents for one synchronized market. It joins
// the best same-outcome bid, never crosses (amends remain post-only), enforces
// the parent's fee-adjusted cap, and limits queue-losing price amendments.
func (e *Engine) HandleBook(ctx context.Context, book domain.OrderBook) []FollowResult {
	if !e.enabled || e.exec == nil {
		return nil
	}
	e.mu.RLock()
	ids := make([]string, 0)
	for id, parent := range e.parents {
		if parent.Strategy == "follow" && parent.Ticker == book.Ticker && !terminal(parent.Status) {
			ids = append(ids, id)
		}
	}
	e.mu.RUnlock()
	results := make([]FollowResult, 0, len(ids))
	for _, id := range ids {
		if result := e.handleFollowParent(ctx, id, book); result.Changed || result.Err != nil {
			results = append(results, result)
		}
	}
	return results
}

// ResumeFollow only clears the manual pause. The caller must immediately feed
// a synchronized current book through HandleBook before treating it as active.
func (e *Engine) ResumeFollow(parentID string) (domain.ParentOrder, error) {
	if !e.enabled || e.exec == nil {
		return domain.ParentOrder{}, ErrDisabled
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	parent, ok := e.parents[parentID]
	if !ok {
		return domain.ParentOrder{}, ErrNotFound
	}
	if parent.Strategy != "follow" || strings.ToLower(strings.TrimSpace(parent.Status)) != "paused" || parent.RemainingRisk <= 0 || activeChildIndex(parent) < 0 {
		return cloneParent(parent), ErrNotResumable
	}
	parent.Status = "working"
	if parent.FilledQuantity > 0 {
		parent.Status = "partially_filled"
	}
	parent.LastRepricedAt = time.Time{}
	parent.UpdatedAt = e.now()
	e.parents[parentID] = parent
	return cloneParent(parent), nil
}

func (e *Engine) handleFollowParent(ctx context.Context, parentID string, book domain.OrderBook) FollowResult {
	e.mu.Lock()
	parent, ok := e.parents[parentID]
	if !ok || parent.Strategy != "follow" || terminal(parent.Status) || strategyStopped(parent.Status) {
		e.mu.Unlock()
		return FollowResult{}
	}
	if book.Stale {
		if parent.Status != "paused_stale" {
			parent.Status = "paused_stale"
			parent.UpdatedAt = e.now()
			e.parents[parentID] = parent
			e.mu.Unlock()
			return FollowResult{Parent: cloneParent(parent), Changed: true}
		}
		e.mu.Unlock()
		return FollowResult{}
	}
	price, ok := bestFollowPrice(book, parent.Side)
	if !ok {
		if parent.Status != "waiting_for_book" {
			parent.Status = "waiting_for_book"
			parent.UpdatedAt = e.now()
			e.parents[parentID] = parent
			e.mu.Unlock()
			return FollowResult{Parent: cloneParent(parent), Changed: true}
		}
		e.mu.Unlock()
		return FollowResult{}
	}
	desiredRemaining, quote, sizingErr := QuantityForCashRisk(price, parent.RemainingRisk)
	if sizingErr != nil || desiredRemaining <= 0 || !withinCap(quote.AllInMoneyline, parent.PriceCapMoneyline) {
		if parent.Status != "price_capped" {
			parent.Status = "price_capped"
			parent.UpdatedAt = e.now()
			e.parents[parentID] = parent
			e.mu.Unlock()
			return FollowResult{Parent: cloneParent(parent), Changed: true}
		}
		e.mu.Unlock()
		return FollowResult{}
	}
	if price == parent.LimitPrice {
		if parent.Status == "paused_stale" || parent.Status == "waiting_for_book" || parent.Status == "price_capped" {
			parent.Status = "working"
			parent.UpdatedAt = e.now()
			e.parents[parentID] = parent
			e.mu.Unlock()
			return FollowResult{Parent: cloneParent(parent), Changed: true}
		}
		e.mu.Unlock()
		return FollowResult{}
	}
	now := e.now()
	if !parent.LastRepricedAt.IsZero() && now.Sub(parent.LastRepricedAt) < FollowRepriceInterval {
		e.mu.Unlock()
		return FollowResult{}
	}
	childIndex := activeChildIndex(parent)
	if childIndex < 0 || !e.enabled || e.exec == nil {
		parent.Status = "paused"
		parent.UpdatedAt = now
		e.parents[parentID] = parent
		e.mu.Unlock()
		return FollowResult{Parent: cloneParent(parent), Changed: true, Err: ErrDisabled}
	}
	child := parent.Children[childIndex]
	updatedClientID, idErr := newID()
	if idErr != nil {
		parent.Status = "paused"
		parent.UpdatedAt = now
		e.parents[parentID] = parent
		e.mu.Unlock()
		return FollowResult{Parent: cloneParent(parent), Changed: true, Err: idErr}
	}
	request := exchange.AmendOrderRequest{
		OrderID: child.ID, Ticker: parent.Ticker, OutcomeSide: parent.Side,
		Quantity: child.FilledQuantity + desiredRemaining, LimitPrice: price,
		ClientOrderID: child.ClientOrderID, UpdatedClientOrderID: updatedClientID,
	}
	parent.Status = "repricing"
	parent.UpdatedAt = now
	e.parents[parentID] = parent
	e.mu.Unlock()

	amended, err := e.exec.AmendOrder(ctx, request)
	if err != nil {
		e.mu.Lock()
		parent = e.parents[parentID]
		if !terminal(parent.Status) && !strategyStopped(parent.Status) {
			parent.Status = "paused"
			parent.UpdatedAt = e.now()
		}
		e.parents[parentID] = parent
		e.mu.Unlock()
		return FollowResult{Parent: cloneParent(parent), Changed: true, Err: err}
	}
	e.mu.Lock()
	parent = e.parents[parentID]
	childIndex = childIndexByID(parent, child.ID)
	if childIndex < 0 {
		parent.Status = "paused"
		parent.UpdatedAt = e.now()
		e.parents[parentID] = parent
		e.mu.Unlock()
		return FollowResult{Parent: cloneParent(parent), Changed: true, Err: errors.New("follow child disappeared during amendment")}
	}
	if terminal(parent.Status) || strategyStopped(parent.Status) || terminal(parent.Children[childIndex].Status) {
		e.parents[parentID] = parent
		e.mu.Unlock()
		return FollowResult{Parent: cloneParent(parent), Changed: true}
	}
	if !terminal(parent.Children[childIndex].Status) {
		parent.Children[childIndex].ClientOrderID = updatedClientID
		parent.Children[childIndex].Quantity = request.Quantity
		parent.Children[childIndex].Status = amended.Status
		if parent.Children[childIndex].Status == "" {
			parent.Children[childIndex].Status = "resting"
		}
		parent.Children[childIndex].UpdatedAt = e.now()
		// An exchange may answer an amend with a replacement order that has a
		// new ID. Track it so later fills, cancels, and risk checks follow the
		// live order; the old ID stays listed so late fills still match.
		if replacement := strings.TrimSpace(amended.ID); replacement != "" && replacement != child.ID {
			parent.Children[childIndex].ID = replacement
			if !contains(parent.ChildOrderIDs, replacement) {
				parent.ChildOrderIDs = append(parent.ChildOrderIDs, replacement)
			}
		}
	}
	actualRemaining := request.Quantity - parent.Children[childIndex].FilledQuantity
	if actualRemaining < 0 {
		actualRemaining = 0
	}
	parent.Quantity = parent.FilledQuantity + actualRemaining
	parent.LimitPrice = price
	parent.ReservedRisk = quote.AllInCost
	if parent.ReservedRisk > parent.RemainingRisk {
		parent.ReservedRisk = parent.RemainingRisk
	}
	parent.Status = "working"
	if parent.FilledQuantity > 0 {
		parent.Status = "partially_filled"
	}
	parent.LastRepricedAt = e.now()
	parent.ReplaceCount++
	parent.UpdatedAt = parent.LastRepricedAt
	e.parents[parentID] = parent
	e.mu.Unlock()
	amended.ID = parent.Children[childIndex].ID
	amended.Exchange = parent.Exchange
	amended.Ticker = parent.Ticker
	amended.Rotation = parent.Rotation
	amended.Market = parent.Market
	amended.Side = parent.Side
	amended.Status = parent.Children[childIndex].Status
	amended.Quantity = request.Quantity
	amended.FilledQuantity = parent.Children[childIndex].FilledQuantity
	amended.LimitPrice = price
	amended.CashRisk = parent.ReservedRisk
	return FollowResult{Parent: cloneParent(parent), Order: &amended, Changed: true}
}

func (e *Engine) Cancel(ctx context.Context, parentID string) (domain.ParentOrder, error) {
	e.mu.Lock()
	parent, ok := e.parents[parentID]
	if !ok {
		e.mu.Unlock()
		return domain.ParentOrder{}, ErrNotFound
	}
	targets := activeChildIDs(parent)
	e.mu.Unlock()
	for _, childID := range targets {
		if err := e.exec.CancelOrder(ctx, childID); err != nil {
			e.mu.RLock()
			current := cloneParent(e.parents[parentID])
			e.mu.RUnlock()
			return current, err
		}
		e.mu.Lock()
		parent = e.parents[parentID]
		setChildStatus(&parent, childID, "canceled")
		e.parents[parentID] = parent
		e.mu.Unlock()
	}
	e.mu.Lock()
	parent = e.parents[parentID]
	parent.Status = "canceled"
	parent.ReservedRisk = 0
	parent.UpdatedAt = time.Now().UTC()
	e.parents[parent.ID] = parent
	e.mu.Unlock()
	return cloneParent(parent), nil
}

func (e *Engine) ApplyFill(fill domain.Fill) (domain.ParentOrder, bool) {
	if fill.OrderID == "" || fill.Quantity <= 0 {
		return domain.ParentOrder{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, parent, matched, _ := e.applyFillLocked(fill)
	return cloneParent(parent), matched
}

func (e *Engine) HandleFill(ctx context.Context, fill domain.Fill) (domain.ParentOrder, *domain.Order, bool, error) {
	if fill.OrderID == "" || fill.Quantity <= 0 {
		return domain.ParentOrder{}, nil, false, nil
	}
	e.mu.Lock()
	parentID, parent, matched, childFilled := e.applyFillLocked(fill)
	if !matched {
		e.mu.Unlock()
		return domain.ParentOrder{}, nil, false, nil
	}
	if parent.FilledQuantity < parent.Quantity && !childFilled && childRiskExceeds(parent, fill.OrderID) {
		if !e.enabled || e.exec == nil {
			parent.Status = "paused"
			parent.UpdatedAt = time.Now().UTC()
			e.parents[parentID] = parent
			e.mu.Unlock()
			return cloneParent(parent), nil, true, nil
		}
		parent.Status = "risk_capping"
		parent.UpdatedAt = time.Now().UTC()
		e.parents[parentID] = parent
		e.mu.Unlock()
		if err := e.exec.CancelOrder(ctx, fill.OrderID); err != nil {
			return e.pauseAfterRefreshError(parentID, err)
		}
		e.mu.Lock()
		parent = e.parents[parentID]
		setChildStatus(&parent, fill.OrderID, "canceled")
		parent.Status = "risk_capped"
		parent.ReservedRisk = 0
		parent.UpdatedAt = time.Now().UTC()
		e.parents[parentID] = parent
		e.mu.Unlock()
		return cloneParent(parent), nil, true, nil
	}
	refresh := parent.Strategy == "iceberg" && childFilled && parent.FilledQuantity < parent.Quantity && !strategyStopped(parent.Status)
	if !refresh {
		e.mu.Unlock()
		return cloneParent(parent), nil, true, nil
	}
	if !e.enabled || e.exec == nil {
		parent.Status = "paused"
		parent.UpdatedAt = time.Now().UTC()
		e.parents[parentID] = parent
		e.mu.Unlock()
		return cloneParent(parent), nil, true, nil
	}
	remaining := parent.Quantity - parent.FilledQuantity
	maxRemaining, remainingQuote, sizingErr := QuantityForCashRisk(parent.LimitPrice, parent.RemainingRisk)
	if sizingErr != nil || maxRemaining <= 0 {
		parent.Status = "risk_capped"
		parent.ReservedRisk = 0
		parent.Quantity = parent.FilledQuantity
		parent.UpdatedAt = time.Now().UTC()
		e.parents[parentID] = parent
		e.mu.Unlock()
		return cloneParent(parent), nil, true, nil
	}
	if maxRemaining < remaining {
		remaining = maxRemaining
		parent.Quantity = parent.FilledQuantity + maxRemaining
		parent.ReservedRisk = remainingQuote.AllInCost
	}
	nextQuantity := parent.SliceQuantity
	if nextQuantity > remaining {
		nextQuantity = remaining
	}
	parent.Status = "refreshing"
	parent.UpdatedAt = time.Now().UTC()
	e.parents[parentID] = parent
	e.mu.Unlock()

	clientOrderID, err := newID()
	if err != nil {
		return e.pauseAfterRefreshError(parentID, err)
	}
	timeInForce, postOnly, ok := policy(parent.Policy)
	if !ok || parent.Policy == "ioc" {
		return e.pauseAfterRefreshError(parentID, ErrInvalidOrder)
	}
	child, err := e.exec.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Ticker: parent.Ticker, OutcomeSide: parent.Side, Quantity: nextQuantity, LimitPrice: parent.LimitPrice,
		TimeInForce: timeInForce, PostOnly: postOnly, ClientOrderID: clientOrderID,
	})
	if err != nil {
		return e.pauseAfterRefreshError(parentID, err)
	}
	e.mu.Lock()
	parent = e.parents[parentID]
	parent.ChildOrderIDs = append(parent.ChildOrderIDs, child.ID)
	parent.Children = append(parent.Children, childState(child, clientOrderID))
	parent.Status = child.Status
	if parent.Status == "" {
		parent.Status = "submitted"
	}
	parent.UpdatedAt = time.Now().UTC()
	e.parents[parentID] = parent
	e.mu.Unlock()
	childCopy := child
	return cloneParent(parent), &childCopy, true, nil
}

func (e *Engine) applyFillLocked(fill domain.Fill) (string, domain.ParentOrder, bool, bool) {
	for id, parent := range e.parents {
		if !contains(parent.ChildOrderIDs, fill.OrderID) || fill.ID != "" && contains(parent.ProcessedFillIDs, fill.ID) {
			continue
		}
		priorStatus := parent.Status
		childFilled := false
		for i := range parent.Children {
			if parent.Children[i].ID != fill.OrderID {
				continue
			}
			parent.Children[i].FilledQuantity += fill.Quantity
			if parent.Children[i].FilledQuantity >= parent.Children[i].Quantity {
				parent.Children[i].Status = "filled"
				childFilled = true
			} else {
				parent.Children[i].Status = "partially_filled"
			}
			parent.Children[i].UpdatedAt = time.Now().UTC()
			break
		}
		parent.FilledQuantity += fill.Quantity
		parent.FilledRisk += fill.CashRisk
		if parent.FilledRisk >= parent.CashRiskTarget {
			parent.RemainingRisk = 0
		} else {
			parent.RemainingRisk = parent.CashRiskTarget - parent.FilledRisk
		}
		if fill.CashRisk >= parent.ReservedRisk {
			parent.ReservedRisk = 0
		} else {
			parent.ReservedRisk -= fill.CashRisk
		}
		if parent.ReservedRisk > parent.RemainingRisk {
			parent.ReservedRisk = parent.RemainingRisk
		}
		if fill.ID != "" {
			parent.ProcessedFillIDs = append(parent.ProcessedFillIDs, fill.ID)
		}
		if parent.FilledQuantity >= parent.Quantity {
			parent.Status = "filled"
			parent.ReservedRisk = 0
		} else if strategyStopped(priorStatus) {
			parent.Status = priorStatus
		} else {
			parent.Status = "partially_filled"
		}
		parent.UpdatedAt = time.Now().UTC()
		e.parents[id] = parent
		return id, parent, true, childFilled
	}
	return "", domain.ParentOrder{}, false, false
}

func (e *Engine) pauseAfterRefreshError(parentID string, cause error) (domain.ParentOrder, *domain.Order, bool, error) {
	e.mu.Lock()
	parent := e.parents[parentID]
	parent.Status = "paused"
	parent.UpdatedAt = time.Now().UTC()
	e.parents[parentID] = parent
	e.mu.Unlock()
	return cloneParent(parent), nil, true, cause
}

func (e *Engine) ApplyOrder(order domain.Order) (domain.ParentOrder, bool) {
	if order.ID == "" {
		return domain.ParentOrder{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, parent := range e.parents {
		if !contains(parent.ChildOrderIDs, order.ID) {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(order.Status))
		setChildStatus(&parent, order.ID, status)
		if parent.Strategy == "iceberg" {
			remainingQuantity := parent.Quantity - parent.FilledQuantity
			if remainingQuantity < 0 {
				remainingQuantity = 0
			}
			if parent.FilledQuantity >= parent.Quantity {
				parent.Status = "filled"
				parent.ReservedRisk = 0
			} else if hasActiveChild(parent) {
				parent.Status = "working"
				if parent.FilledQuantity > 0 || order.FilledQuantity > 0 {
					parent.Status = "partially_filled"
				}
			} else if status == "canceled" || status == "cancelled" || status == "rejected" {
				parent.Status = "paused"
			} else {
				parent.Status = "awaiting_fill"
			}
			if parent.Status != "filled" {
				if quote, err := pricing.Quote(parent.LimitPrice, remainingQuantity, false); err == nil {
					parent.ReservedRisk = quote.AllInCost
					if parent.ReservedRisk > parent.RemainingRisk {
						parent.ReservedRisk = parent.RemainingRisk
					}
				}
			}
		} else {
			switch status {
			case "filled", "executed", "closed":
				parent.Status = "filled"
				parent.ReservedRisk = 0
			case "canceled", "cancelled", "rejected":
				parent.Status = status
				parent.ReservedRisk = 0
			default:
				parent.Status = status
				if parent.Status == "" {
					parent.Status = "submitted"
				}
				if parent.FilledQuantity > 0 || order.FilledQuantity > 0 {
					parent.Status = "partially_filled"
				}
				remainingQuantity := order.Quantity - order.FilledQuantity
				if remainingQuantity < 0 {
					remainingQuantity = 0
				}
				if quote, err := pricing.Quote(parent.LimitPrice, remainingQuantity, false); err == nil {
					parent.ReservedRisk = quote.AllInCost
					if parent.ReservedRisk > parent.RemainingRisk {
						parent.ReservedRisk = parent.RemainingRisk
					}
				} else if remainingQuantity == 0 {
					parent.ReservedRisk = 0
				}
			}
		}
		parent.UpdatedAt = time.Now().UTC()
		e.parents[id] = parent
		return parent, true
	}
	return domain.ParentOrder{}, false
}

// RecordManualAmend keeps a basic parent aligned with an exchange amendment.
// Strategy-managed iceberg/follow children are intentionally excluded because
// their quantities and prices are controlled by the strategy loop.
func (e *Engine) RecordManualAmend(oldOrderID string, order domain.Order, reservedRisk domain.Money) (domain.ParentOrder, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, parent := range e.parents {
		if parent.Strategy != "basic" || !contains(parent.ChildOrderIDs, oldOrderID) {
			continue
		}
		index := childIndexByID(parent, oldOrderID)
		if index < 0 {
			continue
		}
		if order.ID == "" {
			order.ID = oldOrderID
		}
		parent.Children[index].ID = order.ID
		parent.Children[index].Quantity = order.Quantity
		parent.Children[index].FilledQuantity = order.FilledQuantity
		parent.Children[index].Status = order.Status
		parent.Children[index].UpdatedAt = e.now()
		if !contains(parent.ChildOrderIDs, order.ID) {
			parent.ChildOrderIDs = append(parent.ChildOrderIDs, order.ID)
		}
		parent.Quantity = order.Quantity
		parent.FilledQuantity = order.FilledQuantity
		parent.LimitPrice = order.LimitPrice
		parent.ReservedRisk = reservedRisk
		parent.RemainingRisk = reservedRisk
		parent.CashRiskTarget = parent.FilledRisk + reservedRisk
		parent.Status = order.Status
		parent.ReplaceCount++
		parent.UpdatedAt = e.now()
		e.parents[id] = parent
		return cloneParent(parent), true
	}
	return domain.ParentOrder{}, false
}

func QuantityForCashRisk(price, cashRisk domain.Money) (domain.Money, domain.PriceQuote, error) {
	if price <= 0 || price >= domain.Dollar || cashRisk <= 0 {
		return 0, domain.PriceQuote{}, ErrInvalidOrder
	}
	high := domain.Money((int64(cashRisk)*int64(domain.Dollar))/int64(price) + 1)
	low := domain.Money(0)
	for low < high {
		mid := low + (high-low+1)/2
		quote, err := pricing.Quote(price, mid, false)
		if err == nil && quote.AllInCost <= cashRisk {
			low = mid
		} else {
			high = mid - 1
		}
	}
	// Kalshi accepts counts in whole 0.01-contract steps. Rounding down keeps
	// the all-in cost at or under the cash-risk target.
	low = low / domain.ContractStep * domain.ContractStep
	if low == 0 {
		return 0, domain.PriceQuote{}, ErrInvalidOrder
	}
	quote, err := pricing.Quote(price, low, false)
	return low, quote, err
}

func policy(value string) (timeInForce string, postOnly bool, ok bool) {
	switch value {
	case "limit":
		return "good_till_canceled", false, true
	case "post_only":
		return "good_till_canceled", true, true
	case "ioc":
		return "immediate_or_cancel", false, true
	default:
		return "", false, false
	}
}

func withinCap(actual, cap int64) bool {
	actualProbability, actualErr := pricing.ProbabilityFromMoneyline(actual)
	capProbability, capErr := pricing.ProbabilityFromMoneyline(cap)
	return actualErr == nil && capErr == nil && actualProbability <= capProbability
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneParent(parent domain.ParentOrder) domain.ParentOrder {
	parent.ChildOrderIDs = append([]string(nil), parent.ChildOrderIDs...)
	parent.Children = append([]domain.ChildOrderState(nil), parent.Children...)
	parent.ProcessedFillIDs = append([]string(nil), parent.ProcessedFillIDs...)
	return parent
}

func childState(order domain.Order, clientOrderID string) domain.ChildOrderState {
	now := time.Now().UTC()
	created := order.CreatedAt
	if created.IsZero() {
		created = now
	}
	return domain.ChildOrderState{ID: order.ID, ClientOrderID: clientOrderID, Status: order.Status, Quantity: order.Quantity, CreatedAt: created, UpdatedAt: now}
}

func setChildStatus(parent *domain.ParentOrder, childID, status string) {
	for i := range parent.Children {
		if parent.Children[i].ID == childID {
			parent.Children[i].Status = status
			parent.Children[i].UpdatedAt = time.Now().UTC()
			return
		}
	}
}

func activeChildIDs(parent domain.ParentOrder) []string {
	if len(parent.Children) == 0 {
		return append([]string(nil), parent.ChildOrderIDs...)
	}
	result := make([]string, 0, len(parent.Children))
	for _, child := range parent.Children {
		if child.ID != "" && !terminal(child.Status) {
			result = append(result, child.ID)
		}
	}
	return result
}

func hasActiveChild(parent domain.ParentOrder) bool { return len(activeChildIDs(parent)) > 0 }

func activeChildIndex(parent domain.ParentOrder) int {
	for i := len(parent.Children) - 1; i >= 0; i-- {
		if parent.Children[i].ID != "" && !terminal(parent.Children[i].Status) {
			return i
		}
	}
	return -1
}

func childIndexByID(parent domain.ParentOrder, childID string) int {
	for i := range parent.Children {
		if parent.Children[i].ID == childID {
			return i
		}
	}
	return -1
}

func bestFollowPrice(book domain.OrderBook, side string) (domain.Money, bool) {
	if side == "yes" {
		if len(book.Yes) == 0 || book.Yes[0].Price <= 0 || book.Yes[0].Price >= domain.Dollar {
			return 0, false
		}
		return book.Yes[0].Price, true
	}
	if side == "no" {
		if len(book.No) == 0 {
			return 0, false
		}
		price := domain.Dollar - book.No[0].Price
		if price <= 0 || price >= domain.Dollar {
			return 0, false
		}
		return price, true
	}
	return 0, false
}

func childRiskExceeds(parent domain.ParentOrder, childID string) bool {
	remaining := domain.Money(0)
	for _, child := range parent.Children {
		if child.ID == childID {
			if terminal(child.Status) {
				return false
			}
			remaining = child.Quantity - child.FilledQuantity
			break
		}
	}
	if len(parent.Children) == 0 {
		remaining = parent.Quantity - parent.FilledQuantity
	}
	if remaining <= 0 {
		return false
	}
	quote, err := pricing.Quote(parent.LimitPrice, remaining, false)
	return err != nil || quote.AllInCost > parent.RemainingRisk
}

func terminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "filled", "executed", "closed", "canceled", "cancelled", "rejected":
		return true
	default:
		return false
	}
}

func strategyStopped(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "canceled", "cancelled", "rejected", "risk_capped", "paused":
		return true
	default:
		return false
	}
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create client order id: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
