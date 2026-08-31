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

type Adapter interface {
	Name() string
	ListMarkets(context.Context, []domain.CanonicalEvent) ([]domain.CanonicalMarket, error)
	SubscribeAccount(context.Context) (*Subscription, error)
	SubscribeBooks(context.Context, []string) (*Subscription, error)
	Snapshot(context.Context) ([]domain.Order, []domain.Position, []domain.Fill, error)
}
