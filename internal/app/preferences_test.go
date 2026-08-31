package app

import (
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

func TestFilterEventsBySport(t *testing.T) {
	events := []domain.CanonicalEvent{{ID: "1", Sport: "Football"}, {ID: "2", Sport: "Basketball"}, {ID: "3", Sport: "Football"}}
	if got := filterEvents(events, domain.Preferences{}); len(got) != 3 {
		t.Fatalf("unconfigured preferences should include all events, got %d", len(got))
	}
	if got := filterEvents(events, domain.Preferences{EnabledSports: []string{}}); len(got) != 0 {
		t.Fatalf("empty configured preferences should include no events, got %d", len(got))
	}
	got := filterEvents(events, domain.Preferences{EnabledSports: []string{"football"}})
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("unexpected filtered events %+v", got)
	}
}

func TestFilterAddedGames(t *testing.T) {
	events := []domain.CanonicalEvent{{ID: "451", Sport: "Football"}, {ID: "309007", Sport: "Football"}, {ID: "ABC123", Sport: "Football"}}
	got := filterEvents(events, domain.Preferences{ExcludeAddedGames: true})
	if len(got) != 2 || got[0].ID != "451" || got[1].ID != "ABC123" {
		t.Fatalf("unexpected filtered events %+v", got)
	}
}

func TestBuildSettings(t *testing.T) {
	events := []domain.CanonicalEvent{{Sport: "Football"}, {Sport: "Football"}, {Sport: "Basketball"}}
	settings := buildSettings(events, domain.Preferences{EnabledSports: []string{"BASKETBALL"}})
	if len(settings.AvailableSports) != 2 {
		t.Fatalf("got %d sports", len(settings.AvailableSports))
	}
	if settings.AvailableSports[0].Name != "BASKETBALL" || !settings.AvailableSports[0].Enabled || settings.AvailableSports[0].EventCount != 1 {
		t.Fatalf("unexpected first option %+v", settings.AvailableSports[0])
	}
	if settings.AvailableSports[1].Enabled {
		t.Fatalf("football should be disabled")
	}
}

func TestBuildSettingsCountsAddedGames(t *testing.T) {
	events := []domain.CanonicalEvent{{ID: "451", Sport: "Football"}, {ID: "309007", Sport: "Football"}}
	settings := buildSettings(events, domain.Preferences{})
	if settings.AvailableSports[0].EventCount != 2 || settings.AvailableSports[0].AddedGameCount != 1 {
		t.Fatalf("unexpected counts %+v", settings.AvailableSports[0])
	}
}
