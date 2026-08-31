package live

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

var ErrSequenceGap = errors.New("order book sequence gap")

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
	if !ok || delta.Sequence != book.Sequence+1 {
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

func (b *Books) Get(ticker string) (domain.OrderBook, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	book, ok := b.books[ticker]
	return book, ok
}

func sortLevels(book *domain.OrderBook) {
	sort.Slice(book.Yes, func(i, j int) bool { return book.Yes[i].Price > book.Yes[j].Price })
	sort.Slice(book.No, func(i, j int) bool { return book.No[i].Price > book.No[j].Price })
}
