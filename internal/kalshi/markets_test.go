package kalshi

import (
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

func TestSportsbookSeriesUsesEnabledScheduleLeagues(t *testing.T) {
	events := []domain.CanonicalEvent{{League: "College Football"}, {League: "NFL"}, {League: "College Football"}}
	series := sportsbookSeries(events)
	want := map[string]bool{"KXNCAAFGAME": true, "KXNCAAFSPREAD": true, "KXNCAAFTOTAL": true, "KXNFLGAME": true, "KXNFLSPREAD": true, "KXNFLTOTAL": true}
	if len(series) != len(want) {
		t.Fatalf("unexpected series %v", series)
	}
	for _, value := range series {
		if !want[value] {
			t.Fatalf("unexpected series %s", value)
		}
	}
}

func TestCanonicalMarketKeepsEventMatchupAndStrike(t *testing.T) {
	strike := 24.5
	e := event{Title: "UMass vs Rutgers: Spread", Subtitle: "MASS vs RUTG (Sep 3)"}
	m := market{Ticker: "RUTG-SP", Title: "Rutgers wins by over 24.5 points", YesSubTitle: "Rutgers wins by over 24.5 points", YesBid: "0.4900", YesAsk: "0.5100", YesBidSize: "100.00", YesAskSize: "120.00", FloorStrike: &strike}
	got := canonicalMarket(e, m, domain.MarketSpread)
	if got.Title != "UMass vs Rutgers: Spread" || got.Type != domain.MarketSpread || got.Line != "24.5" || got.YesAsk != 5100 || got.YesAskSize != 120*domain.Dollar {
		t.Fatalf("unexpected canonical market %+v", got)
	}
}
