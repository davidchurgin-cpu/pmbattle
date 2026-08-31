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

const MaxDemoCashRisk = 20_000 * domain.Dollar

var (
	ErrDisabled            = errors.New("demo trading is disabled")
	ErrUnsupportedStrategy = errors.New("strategy is not available in this demo release")
	ErrInvalidOrder        = errors.New("invalid parent order")
	ErrPriceCap            = errors.New("fee-adjusted price exceeds the parent order cap")
	ErrNotFound            = errors.New("parent order not found")
)

type Executor interface {
	PlaceOrder(context.Context, exchange.PlaceOrderRequest) (domain.Order, error)
	CancelOrder(context.Context, string) error
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
	mu      sync.RWMutex
	enabled bool
	exec    Executor
	parents map[string]domain.ParentOrder
}

func New(enabled bool, executor Executor) *Engine {
	return &Engine{enabled: enabled, exec: executor, parents: make(map[string]domain.ParentOrder)}
}

func (e *Engine) Create(ctx context.Context, request CreateRequest) (domain.ParentOrder, domain.Order, error) {
	if !e.enabled || e.exec == nil {
		return domain.ParentOrder{}, domain.Order{}, ErrDisabled
	}
	request.Strategy = strings.ToLower(strings.TrimSpace(request.Strategy))
	request.Policy = strings.ToLower(strings.TrimSpace(request.Policy))
	request.Side = strings.ToLower(strings.TrimSpace(request.Side))
	if request.Strategy != "basic" {
		return domain.ParentOrder{}, domain.Order{}, ErrUnsupportedStrategy
	}
	if request.Ticker == "" || request.EventID == "" || request.Outcome == "" || request.Market == "" || request.Side != "yes" && request.Side != "no" || request.LimitPrice <= 0 || request.LimitPrice >= domain.Dollar || request.CashRisk < domain.Dollar || request.CashRisk > MaxDemoCashRisk {
		return domain.ParentOrder{}, domain.Order{}, ErrInvalidOrder
	}
	timeInForce, postOnly, ok := policy(request.Policy)
	if !ok {
		return domain.ParentOrder{}, domain.Order{}, ErrInvalidOrder
	}
	quantity, quote, err := QuantityForCashRisk(request.LimitPrice, request.CashRisk)
	if err != nil || quantity <= 0 {
		return domain.ParentOrder{}, domain.Order{}, ErrInvalidOrder
	}
	if !withinCap(quote.AllInMoneyline, request.PriceCapMoneyline) {
		return domain.ParentOrder{}, domain.Order{}, ErrPriceCap
	}
	id, err := newID()
	if err != nil {
		return domain.ParentOrder{}, domain.Order{}, err
	}
	now := time.Now().UTC()
	parent := domain.ParentOrder{
		ID: id, Exchange: "Kalshi", EventID: request.EventID, Ticker: request.Ticker,
		Rotation: request.Rotation, Outcome: request.Outcome, Market: request.Market, Side: request.Side,
		Strategy: request.Strategy, Policy: request.Policy, Status: "submitting", CashRiskTarget: request.CashRisk,
		ReservedRisk: quote.AllInCost, RemainingRisk: request.CashRisk, PriceCapMoneyline: request.PriceCapMoneyline,
		LimitPrice: request.LimitPrice, Quantity: quantity, CreatedAt: now, UpdatedAt: now,
	}
	child, err := e.exec.PlaceOrder(ctx, exchange.PlaceOrderRequest{
		Ticker: request.Ticker, OutcomeSide: request.Side, Quantity: quantity, LimitPrice: request.LimitPrice,
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
	parent.UpdatedAt = time.Now().UTC()
	e.mu.Lock()
	e.parents[parent.ID] = parent
	e.mu.Unlock()
	return parent, child, nil
}

func (e *Engine) Cancel(ctx context.Context, parentID string) (domain.ParentOrder, error) {
	e.mu.RLock()
	parent, ok := e.parents[parentID]
	e.mu.RUnlock()
	if !ok {
		return domain.ParentOrder{}, ErrNotFound
	}
	for _, childID := range parent.ChildOrderIDs {
		if err := e.exec.CancelOrder(ctx, childID); err != nil {
			return parent, err
		}
	}
	parent.Status = "canceled"
	parent.ReservedRisk = 0
	parent.UpdatedAt = time.Now().UTC()
	e.mu.Lock()
	e.parents[parent.ID] = parent
	e.mu.Unlock()
	return parent, nil
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
