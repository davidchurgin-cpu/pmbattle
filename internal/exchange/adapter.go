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
	ListMarkets(context.Context) ([]domain.CanonicalMarket, error)
	Subscribe(context.Context, []string) (*Subscription, error)
	Snapshot(context.Context) ([]domain.Order, []domain.Position, []domain.Fill, error)
}
