package orders

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
)

type fakeExecutor struct {
	placed    []exchange.PlaceOrderRequest
	canceled  []string
	failPlace int
}

func (f *fakeExecutor) PlaceOrder(_ context.Context, request exchange.PlaceOrderRequest) (domain.Order, error) {
	f.placed = append(f.placed, request)
	if f.failPlace > 0 && len(f.placed) >= f.failPlace {
		return domain.Order{}, errors.New("exchange unavailable")
	}
	id := fmt.Sprintf("child-%d", len(f.placed))
	return domain.Order{ID: id, Exchange: "Kalshi", Ticker: request.Ticker, Side: request.OutcomeSide, Status: "resting", Quantity: request.Quantity, LimitPrice: request.LimitPrice}, nil
}
func (f *fakeExecutor) CancelOrder(_ context.Context, id string) error {
	f.canceled = append(f.canceled, id)
	return nil
}

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
	if child.ID != "child-1" || executor.placed[0].ClientOrderID != parent.ID {
		t.Fatalf("parent/child linkage missing: parent=%+v child=%+v request=%+v", parent, child, executor.placed)
	}
	nextQuote, err := pricing.Quote(parent.LimitPrice, parent.Quantity+1, false)
	if err != nil || nextQuote.AllInCost <= parent.CashRiskTarget {
		t.Fatalf("quantity was not maximal: quantity=%d next=%+v err=%v", parent.Quantity, nextQuote, err)
	}
	if _, err := engine.Cancel(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
	if len(executor.canceled) != 1 || executor.canceled[0] != child.ID {
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
	request.Strategy = "follow"
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

func TestIcebergExposesOneSliceAndRefreshesOnlyAfterFullSlice(t *testing.T) {
	executor := &fakeExecutor{}
	engine := New(true, executor)
	request := validRequest()
	request.Strategy = "iceberg"
	request.Policy = "post_only"
	request.SliceQuantity = 25 * domain.Dollar
	parent, firstChild, err := engine.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if firstChild.Quantity != request.SliceQuantity || parent.Quantity <= firstChild.Quantity || len(parent.Children) != 1 || len(executor.placed) != 1 {
		t.Fatalf("iceberg exposed wrong initial state: parent=%+v child=%+v placements=%+v", parent, firstChild, executor.placed)
	}
	partialQuote, _ := pricing.Quote(parent.LimitPrice, 10*domain.Dollar, false)
	partial := domain.Fill{ID: "fill-1", OrderID: firstChild.ID, Quantity: 10 * domain.Dollar, CashRisk: partialQuote.AllInCost}
	updated, refreshed, matched, err := engine.HandleFill(context.Background(), partial)
	if err != nil || !matched || refreshed != nil || len(executor.placed) != 1 || updated.Children[0].Status != "partially_filled" {
		t.Fatalf("partial fill refreshed early: parent=%+v child=%+v matched=%v err=%v placements=%d", updated, refreshed, matched, err, len(executor.placed))
	}
	restQuote, _ := pricing.Quote(parent.LimitPrice, 15*domain.Dollar, false)
	rest := domain.Fill{ID: "fill-2", OrderID: firstChild.ID, Quantity: 15 * domain.Dollar, CashRisk: restQuote.AllInCost}
	updated, refreshed, matched, err = engine.HandleFill(context.Background(), rest)
	if err != nil || !matched || refreshed == nil || refreshed.ID != "child-2" || refreshed.Quantity != request.SliceQuantity || len(executor.placed) != 2 || len(updated.Children) != 2 || updated.Children[0].Status != "filled" {
		t.Fatalf("full slice did not refresh exactly once: parent=%+v child=%+v matched=%v err=%v placements=%+v", updated, refreshed, matched, err, executor.placed)
	}
	if updated.FilledRisk+updated.ReservedRisk > updated.CashRiskTarget {
		t.Fatalf("iceberg exceeded risk target: filled=%d reserved=%d target=%d", updated.FilledRisk, updated.ReservedRisk, updated.CashRiskTarget)
	}
	if _, duplicateChild, duplicateMatched, duplicateErr := engine.HandleFill(context.Background(), rest); duplicateErr != nil || duplicateMatched || duplicateChild != nil || len(executor.placed) != 2 {
		t.Fatalf("duplicate fill refreshed another slice: child=%+v matched=%v err=%v placements=%d", duplicateChild, duplicateMatched, duplicateErr, len(executor.placed))
	}
	canceled, err := engine.Cancel(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != "canceled" || len(executor.canceled) != 1 || executor.canceled[0] != "child-2" {
		t.Fatalf("cancel touched wrong slices: parent=%+v canceled=%v", canceled, executor.canceled)
	}
}

func TestIcebergRefreshFailurePausesWithoutPhantomChild(t *testing.T) {
	executor := &fakeExecutor{failPlace: 2}
	engine := New(true, executor)
	request := validRequest()
	request.Strategy = "iceberg"
	request.Policy = "post_only"
	request.SliceQuantity = 10 * domain.Dollar
	parent, child, err := engine.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	fillQuote, _ := pricing.Quote(parent.LimitPrice, child.Quantity, false)
	updated, refreshed, matched, err := engine.HandleFill(context.Background(), domain.Fill{ID: "fill-1", OrderID: child.ID, Quantity: child.Quantity, CashRisk: fillQuote.AllInCost})
	if err == nil || !matched || refreshed != nil || updated.Status != "paused" || len(updated.Children) != 1 {
		t.Fatalf("refresh failure was not safely paused: parent=%+v child=%+v matched=%v err=%v", updated, refreshed, matched, err)
	}
}

func TestIcebergRequiresSliceAndRejectsIOC(t *testing.T) {
	engine := New(true, &fakeExecutor{})
	request := validRequest()
	request.Strategy = "iceberg"
	if _, _, err := engine.Create(context.Background(), request); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("missing slice got %v", err)
	}
	request.SliceQuantity = 10 * domain.Dollar
	request.Policy = "ioc"
	if _, _, err := engine.Create(context.Background(), request); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("iceberg IOC got %v", err)
	}
}

func TestUnexpectedFillCostCancelsOutstandingChildBeforeRiskBreach(t *testing.T) {
	executor := &fakeExecutor{}
	engine := New(true, executor)
	request := validRequest()
	request.CashRisk = 100 * domain.Dollar
	parent, child, err := engine.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	fill := domain.Fill{ID: "fill-1", OrderID: child.ID, Quantity: domain.Dollar, CashRisk: parent.CashRiskTarget - domain.Dollar}
	updated, refreshed, matched, err := engine.HandleFill(context.Background(), fill)
	if err != nil || !matched || refreshed != nil || updated.Status != "risk_capped" || updated.ReservedRisk != 0 || len(executor.canceled) != 1 || executor.canceled[0] != child.ID {
		t.Fatalf("risk breach was not capped: parent=%+v child=%+v matched=%v canceled=%v err=%v", updated, refreshed, matched, executor.canceled, err)
	}
	if updated.FilledRisk+updated.ReservedRisk > updated.CashRiskTarget {
		t.Fatalf("risk exceeded target: %+v", updated)
	}
}

func TestLateFillAfterIcebergCancelUpdatesRiskWithoutRestart(t *testing.T) {
	executor := &fakeExecutor{}
	engine := New(true, executor)
	request := validRequest()
	request.Strategy = "iceberg"
	request.Policy = "post_only"
	request.SliceQuantity = 10 * domain.Dollar
	parent, child, err := engine.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Cancel(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
	fillQuote, _ := pricing.Quote(parent.LimitPrice, child.Quantity, false)
	updated, refreshed, matched, err := engine.HandleFill(context.Background(), domain.Fill{ID: "late-fill", OrderID: child.ID, Quantity: child.Quantity, CashRisk: fillQuote.AllInCost})
	if err != nil || !matched || refreshed != nil || updated.Status != "canceled" || updated.FilledRisk != fillQuote.AllInCost || len(executor.placed) != 1 || len(executor.canceled) != 1 {
		t.Fatalf("late fill restarted canceled iceberg: parent=%+v child=%+v matched=%v placed=%d canceled=%d err=%v", updated, refreshed, matched, len(executor.placed), len(executor.canceled), err)
	}
}
