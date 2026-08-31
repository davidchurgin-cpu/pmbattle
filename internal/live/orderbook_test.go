package live

import (
	"errors"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

func TestOrderBookAcceptsSubscriptionWideSequences(t *testing.T) {
	books := NewBooks()
	books.Snapshot(domain.OrderBook{Ticker: "TEST", Sequence: 10, Yes: []domain.BookLevel{{Price: 5000, Quantity: 10000}}})
	book, err := books.Apply(domain.OrderBookDelta{Ticker: "TEST", Sequence: 11, Side: "yes", Price: 5000, Delta: -2000})
	if err != nil {
		t.Fatal(err)
	}
	if book.Yes[0].Quantity != 8000 {
		t.Fatalf("quantity got %d", book.Yes[0].Quantity)
	}
	book, err = books.Apply(domain.OrderBookDelta{Ticker: "TEST", Sequence: 13, Side: "yes", Price: 5100, Delta: 1000})
	if err != nil || book.Stale || book.Sequence != 13 {
		t.Fatalf("subscription-wide sequence should be accepted: book=%+v err=%v", book, err)
	}
}

func TestOrderBookRequiresSnapshot(t *testing.T) {
	books := NewBooks()
	book, err := books.Apply(domain.OrderBookDelta{Ticker: "TEST", Sequence: 13, Side: "yes", Price: 5100, Delta: 1000})
	if !errors.Is(err, ErrSequenceGap) || !book.Stale {
		t.Fatalf("expected missing-snapshot stale book")
	}
}
