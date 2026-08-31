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
