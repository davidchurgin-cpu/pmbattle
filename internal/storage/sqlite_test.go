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
