package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

func TestEventsRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := domain.CanonicalEvent{ID: "1", Sport: "Football", League: "NFL", StartTime: time.Now().UTC().Truncate(time.Second), Participants: []domain.Participant{{Rotation: "451", Name: "Team A"}}}
	if err := store.SaveEvents(context.Background(), []domain.CanonicalEvent{want}); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want.ID || got[0].Participants[0].Rotation != "451" {
		t.Fatalf("unexpected events %+v", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.SetSetting(ctx, "preferences", `{"enabledSports":["FOOTBALL"]}`); err != nil {
		t.Fatal(err)
	}
	value, ok, err := store.GetSetting(ctx, "preferences")
	if err != nil || !ok || value != `{"enabledSports":["FOOTBALL"]}` {
		t.Fatalf("unexpected setting value=%q ok=%v err=%v", value, ok, err)
	}
}

func TestParentOrdersRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "parents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := domain.ParentOrder{ID: "parent-1", Ticker: "TEST", Side: "yes", Strategy: "iceberg", Status: "partially_filled", CashRiskTarget: 5_000 * domain.Dollar, FilledRisk: 500 * domain.Dollar, RemainingRisk: 4_500 * domain.Dollar, SliceQuantity: 25 * domain.Dollar, ChildOrderIDs: []string{"child-1"}, Children: []domain.ChildOrderState{{ID: "child-1", Status: "partially_filled", Quantity: 25 * domain.Dollar, FilledQuantity: 10 * domain.Dollar}}, ProcessedFillIDs: []string{"fill-1"}, UpdatedAt: time.Now().UTC()}
	if err := store.SaveParentOrder(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadParentOrders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want.ID || got[0].RemainingRisk != want.RemainingRisk || got[0].SliceQuantity != want.SliceQuantity || len(got[0].Children) != 1 || got[0].Children[0].FilledQuantity != 10*domain.Dollar || len(got[0].ProcessedFillIDs) != 1 {
		t.Fatalf("unexpected parents %+v", got)
	}
}

func TestSettlementsRoundTripAndExchangeCursor(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settlements.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	first := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	want := []domain.Settlement{
		{Exchange: "Kalshi", Ticker: "A", Result: "yes", Revenue: 100 * domain.Dollar, NetPnL: 20 * domain.Dollar, SettledAt: first},
		{Exchange: "Kalshi", Ticker: "B", Result: "no", Revenue: 0, NetPnL: -40 * domain.Dollar, SettledAt: second},
		{Exchange: "Other", Ticker: "C", SettledAt: second.Add(time.Hour)},
	}
	if err := store.SaveSettlements(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSettlements(ctx, 2)
	if err != nil || len(got) != 2 || got[0].Ticker != "C" || got[1].Ticker != "B" {
		t.Fatalf("unexpected settlement history %+v err=%v", got, err)
	}
	latest, err := store.LatestSettlementTime(ctx, "Kalshi")
	if err != nil || !latest.Equal(second) {
		t.Fatalf("unexpected Kalshi cursor %v err=%v", latest, err)
	}
	want[1].NetPnL = -35 * domain.Dollar
	if err := store.SaveSettlements(ctx, []domain.Settlement{want[1]}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.LoadSettlements(ctx, 3)
	if got[1].Ticker != "B" || got[1].NetPnL != want[1].NetPnL {
		t.Fatalf("settlement upsert failed %+v", got)
	}
}
