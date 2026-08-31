package pricing

import (
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

func TestMoneylineFromProbability(t *testing.T) {
	tests := []struct {
		prob domain.Money
		want int64
	}{{5000, 100}, {4000, 150}, {6000, -150}, {5238, -110}}
	for _, tt := range tests {
		got, err := MoneylineFromProbability(tt.prob)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("probability %d: got %d want %d", tt.prob, got, tt.want)
		}
	}
}

func TestKalshiFee(t *testing.T) {
	price := domain.Money(5000)
	quantity := domain.Money(100 * domain.Dollar)
	taker := KalshiFee(price, quantity, false)
	maker := KalshiFee(price, quantity, true)
	if taker != 17500 {
		t.Fatalf("taker fee got %d want 17500", taker)
	}
	if maker != 4375 {
		t.Fatalf("maker fee got %d want 4375", maker)
	}
}

func TestQuoteIncludesFees(t *testing.T) {
	quote, err := Quote(5000, 100*domain.Dollar, false)
	if err != nil {
		t.Fatal(err)
	}
	if quote.AllInCost != 517500 {
		t.Fatalf("all-in cost got %d", quote.AllInCost)
	}
	if quote.AllInMoneyline >= quote.RawMoneyline {
		t.Fatalf("expected fee-adjusted line to be more negative: raw=%d all-in=%d", quote.RawMoneyline, quote.AllInMoneyline)
	}
}
