package orders

import (
	"context"
	"errors"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
)

type fakeExecutor struct {
	placed   exchange.PlaceOrderRequest
	canceled string
}

func (f *fakeExecutor) PlaceOrder(_ context.Context, request exchange.PlaceOrderRequest) (domain.Order, error) {
	f.placed = request
	return domain.Order{ID: "child-1", Exchange: "Kalshi", Ticker: request.Ticker, Side: request.OutcomeSide, Status: "resting", Quantity: request.Quantity, LimitPrice: request.LimitPrice}, nil
}
func (f *fakeExecutor) CancelOrder(_ context.Context, id string) error { f.canceled = id; return nil }

func validRequest() CreateRequest {
	return CreateRequest{EventID: "1234", Ticker: "KXTEST", Outcome: "Over", Market: "total", Side: "yes", Strategy: "basic", Policy: "limit", CashRisk: 5_000 * domain.Dollar, PriceCapMoneyline: -107, LimitPrice: 5000}
}

func TestCreateBasicOrderNeverExceedsCashRisk(t *testing.T) {
	executor := &fakeExecutor{}
	engine := New(true, executor)
	parent, child, err := engine.Create(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if parent.ReservedRisk > parent.CashRiskTarget {
		t.Fatalf("reserved risk %d exceeds target %d", parent.ReservedRisk, parent.CashRiskTarget)
	}
	if child.ID != "child-1" || executor.placed.ClientOrderID != parent.ID {
		t.Fatalf("parent/child linkage missing: parent=%+v child=%+v request=%+v", parent, child, executor.placed)
	}
	nextQuote, err := pricing.Quote(parent.LimitPrice, parent.Quantity+1, false)
	if err != nil || nextQuote.AllInCost <= parent.CashRiskTarget {
		t.Fatalf("quantity was not maximal: quantity=%d next=%+v err=%v", parent.Quantity, nextQuote, err)
	}
	if _, err := engine.Cancel(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
	if executor.canceled != child.ID {
		t.Fatalf("canceled %q, want %q", executor.canceled, child.ID)
	}
}

func TestEngineIsHardDisabled(t *testing.T) {
	_, _, err := New(false, &fakeExecutor{}).Create(context.Background(), validRequest())
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("got %v, want disabled", err)
	}
}

func TestRejectsUnsupportedStrategyAndPriceCap(t *testing.T) {
	engine := New(true, &fakeExecutor{})
	request := validRequest()
	request.Strategy = "iceberg"
	if _, _, err := engine.Create(context.Background(), request); !errors.Is(err, ErrUnsupportedStrategy) {
		t.Fatalf("got %v, want unsupported strategy", err)
	}
	request = validRequest()
	request.PriceCapMoneyline = 105
	if _, _, err := engine.Create(context.Background(), request); !errors.Is(err, ErrPriceCap) {
		t.Fatalf("got %v, want price cap", err)
	}
}

func TestQuantitySizingAtLowPriceDoesNotOverflow(t *testing.T) {
	quantity, quote, err := QuantityForCashRisk(100, 5_000*domain.Dollar)
	if err != nil {
		t.Fatal(err)
	}
	if quantity <= 0 || quote.AllInCost > 5_000*domain.Dollar {
		t.Fatalf("quantity=%d quote=%+v", quantity, quote)
	}
}
