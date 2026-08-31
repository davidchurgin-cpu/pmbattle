package live

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

var ErrSequenceGap = errors.New("order book delta received before snapshot")

type Books struct {
	mu    sync.RWMutex
	books map[string]domain.OrderBook
}

func NewBooks() *Books { return &Books{books: make(map[string]domain.OrderBook)} }

func (b *Books) Snapshot(book domain.OrderBook) {
	b.mu.Lock()
	defer b.mu.Unlock()
	book.Stale = false
	book.UpdatedAt = time.Now().UTC()
	sortLevels(&book)
	b.books[book.Ticker] = book
}

func (b *Books) Apply(delta domain.OrderBookDelta) (domain.OrderBook, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	book, ok := b.books[delta.Ticker]
	if !ok {
		book.Stale = true
		b.books[delta.Ticker] = book
		return book, ErrSequenceGap
	}
	levels := &book.Yes
	if delta.Side == "no" {
		levels = &book.No
	}
	found := false
	for i := range *levels {
		if (*levels)[i].Price == delta.Price {
			(*levels)[i].Quantity += delta.Delta
			if (*levels)[i].Quantity <= 0 {
				*levels = append((*levels)[:i], (*levels)[i+1:]...)
			}
			found = true
			break
		}
	}
	if !found && delta.Delta > 0 {
		*levels = append(*levels, domain.BookLevel{Price: delta.Price, Quantity: delta.Delta})
	}
	book.Sequence = delta.Sequence
	book.UpdatedAt = time.Now().UTC()
	sortLevels(&book)
	b.books[delta.Ticker] = book
	return book, nil
}

func (b *Books) MarkAllStale() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ticker, book := range b.books {
		book.Stale = true
		b.books[ticker] = book
	}
}

func (b *Books) MarkStale(ticker string) (domain.OrderBook, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	book, ok := b.books[ticker]
	if !ok {
		return domain.OrderBook{}, false
	}
	book.Stale = true
	b.books[ticker] = book
	return book, true
}

func (b *Books) Get(ticker string) (domain.OrderBook, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	book, ok := b.books[ticker]
	return book, ok
}

func sortLevels(book *domain.OrderBook) {
	sort.Slice(book.Yes, func(i, j int) bool { return book.Yes[i].Price > book.Yes[j].Price })
	// With use_yes_price enabled, Kalshi's no side is the YES ask ladder.
	// Lowest ask belongs nearest the center of the book.
	sort.Slice(book.No, func(i, j int) bool { return book.No[i].Price < book.No[j].Price })
}
