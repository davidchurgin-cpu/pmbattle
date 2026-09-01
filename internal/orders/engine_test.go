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

func TestFillReducesParentRiskOnceBeforePublication(t *testing.T) {
	engine := New(true, &fakeExecutor{})
	parent, child, err := engine.Create(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	fillQuote, err := pricing.Quote(parent.LimitPrice, 10*domain.Dollar, false)
	if err != nil {
		t.Fatal(err)
	}
	fill := domain.Fill{ID: "fill-1", OrderID: child.ID, Quantity: 10 * domain.Dollar, CashRisk: fillQuote.AllInCost}
	updated, ok := engine.ApplyFill(fill)
	if !ok {
		t.Fatal("fill did not match parent")
	}
	if updated.Status != "partially_filled" || updated.FilledQuantity != fill.Quantity || updated.FilledRisk != fill.CashRisk || updated.RemainingRisk != parent.CashRiskTarget-fill.CashRisk || updated.ReservedRisk != parent.ReservedRisk-fill.CashRisk {
		t.Fatalf("unexpected reconciled parent %+v", updated)
	}
	duplicate, ok := engine.ApplyFill(fill)
	if ok || duplicate.ID != "" {
		t.Fatalf("duplicate fill was applied: %+v ok=%v", duplicate, ok)
	}
}

func TestRestoredParentReconcilesChildCancellation(t *testing.T) {
	parent := domain.ParentOrder{ID: "parent-1", Ticker: "TEST", Status: "resting", CashRiskTarget: 100 * domain.Dollar, RemainingRisk: 100 * domain.Dollar, ReservedRisk: 90 * domain.Dollar, LimitPrice: 5000, Quantity: 190 * domain.Dollar, ChildOrderIDs: []string{"child-1"}}
	engine := New(true, &fakeExecutor{})
	engine.Restore([]domain.ParentOrder{parent})
	updated, ok := engine.ApplyOrder(domain.Order{ID: "child-1", Status: "canceled", Quantity: parent.Quantity, FilledQuantity: 10 * domain.Dollar})
	if !ok || updated.Status != "canceled" || updated.ReservedRisk != 0 || updated.FilledQuantity != 0 {
		t.Fatalf("unexpected restored parent %+v ok=%v", updated, ok)
	}
}

func TestOrderSnapshotDoesNotDoubleCountReplayedFills(t *testing.T) {
	parent := domain.ParentOrder{ID: "parent-1", Status: "resting", CashRiskTarget: 100 * domain.Dollar, RemainingRisk: 100 * domain.Dollar, ReservedRisk: 90 * domain.Dollar, LimitPrice: 5000, Quantity: 190 * domain.Dollar, ChildOrderIDs: []string{"child-1"}}
	engine := New(true, &fakeExecutor{})
	engine.Restore([]domain.ParentOrder{parent})
	if _, ok := engine.ApplyOrder(domain.Order{ID: "child-1", Status: "resting", Quantity: parent.Quantity, FilledQuantity: 10 * domain.Dollar}); !ok {
		t.Fatal("order snapshot did not match parent")
	}
	fillQuote, _ := pricing.Quote(parent.LimitPrice, 10*domain.Dollar, false)
	updated, ok := engine.ApplyFill(domain.Fill{ID: "fill-1", OrderID: "child-1", Quantity: 10 * domain.Dollar, CashRisk: fillQuote.AllInCost})
	if !ok || updated.FilledQuantity != 10*domain.Dollar {
		t.Fatalf("filled quantity was double-counted: %+v", updated)
	}
}
