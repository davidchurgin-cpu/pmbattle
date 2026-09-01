package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/storage"
)

func TestFillReconcilesAndPersistsParentBeforeBrowserEvent(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "reconcile.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := New(Config{}, store, nil)
	parent := domain.ParentOrder{ID: "parent-1", EventID: "event-1", Ticker: "TEST", Rotation: "451", Outcome: "Team A", Market: "moneyline", Side: "yes", Status: "resting", CashRiskTarget: 100 * domain.Dollar, ReservedRisk: 90 * domain.Dollar, RemainingRisk: 100 * domain.Dollar, LimitPrice: 5000, Quantity: 190 * domain.Dollar, ChildOrderIDs: []string{"child-1"}}
	service.orderEngine.Restore([]domain.ParentOrder{parent})
	service.snapshot.ParentOrders = []domain.ParentOrder{parent}
	events, cancel := service.Subscribe()
	defer cancel()
	fill := domain.Fill{ID: "fill-1", OrderID: "child-1", Exchange: "Kalshi", Ticker: "TEST", Quantity: 10 * domain.Dollar, CashRisk: 5 * domain.Dollar}
	service.handleExchangeEvent(domain.StreamEvent{Type: "fill", Data: fill})
	first, second := <-events, <-events
	if first.Type != "parent_order" || second.Type != "fill" {
		t.Fatalf("browser event order = %q then %q", first.Type, second.Type)
	}
	snapshot := service.Snapshot()
	if len(snapshot.ParentOrders) != 1 || snapshot.ParentOrders[0].FilledRisk != fill.CashRisk || snapshot.ParentOrders[0].RemainingRisk != parent.CashRiskTarget-fill.CashRisk || snapshot.AtRisk != parent.ReservedRisk {
		t.Fatalf("risk was not reconciled before publication: %+v", snapshot.ParentOrders)
	}
	persisted, err := store.LoadParentOrders(context.Background())
	if err != nil || len(persisted) != 1 || persisted[0].ProcessedFillIDs[0] != fill.ID {
		t.Fatalf("parent was not persisted: %+v err=%v", persisted, err)
	}
	service.handleExchangeEvent(domain.StreamEvent{Type: "fill", Data: fill})
	select {
	case duplicate := <-events:
		t.Fatalf("duplicate fill was published: %+v", duplicate)
	default:
	}
}
