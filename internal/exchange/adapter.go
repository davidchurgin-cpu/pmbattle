package exchange

import (
	"context"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

type Subscription struct {
	Events <-chan domain.StreamEvent
	Errors <-chan error
	Close  func() error
}

type PlaceOrderRequest struct {
	Ticker        string
	OutcomeSide   string
	Quantity      domain.Money
	LimitPrice    domain.Money
	TimeInForce   string
	PostOnly      bool
	ClientOrderID string
}

type Adapter interface {
	Name() string
	ListMarkets(context.Context, []domain.CanonicalEvent) ([]domain.CanonicalMarket, error)
	SubscribeAccount(context.Context) (*Subscription, error)
	SubscribeBooks(context.Context, []string) (*Subscription, error)
	Snapshot(context.Context) ([]domain.Order, []domain.Position, []domain.Fill, error)
	Balance(context.Context) (domain.Money, error)
	Fills(context.Context, []string) ([]domain.Fill, error)
	PlaceOrder(context.Context, PlaceOrderRequest) (domain.Order, error)
	CancelOrder(context.Context, string) error
}
