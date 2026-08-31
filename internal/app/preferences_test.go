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

func TestAttachSimulatedMarketsIncludesAllSportsbookColumns(t *testing.T) {
	events := []domain.CanonicalEvent{{ID: "451", Participants: []domain.Participant{{Name: "Away"}, {Name: "Home"}}}}
	attachSimulatedMarkets(events)
	if len(events[0].Markets) != 3 {
		t.Fatalf("expected moneyline, spread, and total markets, got %+v", events[0].Markets)
	}
	if events[0].Markets[0].Type != domain.MarketMoneyline || events[0].Markets[0].Away == nil || events[0].Markets[0].Home == nil {
		t.Fatalf("invalid moneyline market %+v", events[0].Markets[0])
	}
	if events[0].Markets[1].Type != domain.MarketSpread || events[0].Markets[1].Line == "" || events[0].Markets[1].Away == nil || events[0].Markets[1].Home == nil {
		t.Fatalf("invalid spread market %+v", events[0].Markets[1])
	}
	if events[0].Markets[2].Type != domain.MarketTotal || events[0].Markets[2].Line == "" || events[0].Markets[2].Over == nil || events[0].Markets[2].Under == nil {
		t.Fatalf("invalid total market %+v", events[0].Markets[2])
	}
}

func TestAttachSimulatedMarketsUsesLowerAddedGameLimits(t *testing.T) {
	events := []domain.CanonicalEvent{
		{ID: "451", Participants: []domain.Participant{{Name: "Away"}, {Name: "Home"}}},
		{ID: "309007", Participants: []domain.Participant{{Name: "Away"}, {Name: "Home"}}},
	}
	attachSimulatedMarkets(events)
	regular := events[0].Markets[0].Away.AvailableQuantity
	added := events[1].Markets[0].Away.AvailableQuantity
	if added >= regular {
		t.Fatalf("expected added-game limit below regular limit, got added=%d regular=%d", added, regular)
	}
}

func TestAttachMatchedBuildsLiveSportsbookMarkets(t *testing.T) {
	service := &Service{snapshot: domain.Snapshot{Events: []domain.CanonicalEvent{{
		ID: "141", Participants: []domain.Participant{{Name: "Massachusetts"}, {Name: "Rutgers"}},
	}}}}
	markets := []domain.CanonicalMarket{
		{EventID: "141", MappingStatus: "accepted", Exchange: "kalshi", ExchangeTicker: "MASS-ML", Type: domain.MarketMoneyline, Outcome: "UMass", YesBid: 200, YesAsk: 300, YesBidSize: 100 * domain.Dollar, YesAskSize: 100 * domain.Dollar},
		{EventID: "141", MappingStatus: "accepted", Exchange: "kalshi", ExchangeTicker: "RUTG-ML", Type: domain.MarketMoneyline, Outcome: "Rutgers", YesBid: 9700, YesAsk: 9800, YesBidSize: 100 * domain.Dollar, YesAskSize: 100 * domain.Dollar},
		{EventID: "141", MappingStatus: "accepted", Exchange: "kalshi", ExchangeTicker: "RUTG-SP", Type: domain.MarketSpread, Outcome: "Rutgers wins by over 24.5 points", Line: "24.5", YesBid: 4900, YesAsk: 5100, YesBidSize: 100 * domain.Dollar, YesAskSize: 100 * domain.Dollar},
		{EventID: "141", MappingStatus: "accepted", Exchange: "kalshi", ExchangeTicker: "GAME-TOTAL", Type: domain.MarketTotal, Outcome: "Over 52.5 points", Line: "52.5", YesBid: 4800, YesAsk: 5200, YesBidSize: 100 * domain.Dollar, YesAskSize: 100 * domain.Dollar},
	}
	tickers := service.attachMatched(markets)
	views := service.snapshot.Events[0].Markets
	if len(views) != 3 || views[0].Away == nil || views[0].Home == nil {
		t.Fatalf("unexpected live views %+v", views)
	}
	if views[1].Line != "-24.5" || views[1].Away == nil || views[1].Home == nil {
		t.Fatalf("unexpected spread %+v", views[1])
	}
	if views[2].Line != "52.5" || views[2].Over == nil || views[2].Under == nil {
		t.Fatalf("unexpected total %+v", views[2])
	}
	if len(tickers) != 4 {
		t.Fatalf("expected four selected tickers, got %v", tickers)
	}
}

func TestSetEventsPreservesVerifiedLiveMarkets(t *testing.T) {
	liveQuote := &domain.PriceQuote{Ticker: "KXNCAAFGAME-LIVE"}
	service := &Service{snapshot: domain.Snapshot{Events: []domain.CanonicalEvent{{ID: "141", Markets: []domain.MarketView{{Type: domain.MarketMoneyline, Home: liveQuote}}}}}}
	service.setEvents([]domain.CanonicalEvent{{ID: "141"}}, false)
	if len(service.snapshot.Events[0].Markets) != 1 || service.snapshot.Events[0].Markets[0].Home.Ticker != "KXNCAAFGAME-LIVE" {
		t.Fatalf("live market was not preserved: %+v", service.snapshot.Events[0].Markets)
	}
}
