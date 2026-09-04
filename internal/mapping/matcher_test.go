package mapping

import (
	"testing"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

func TestMatch(t *testing.T) {
	events := []domain.CanonicalEvent{{ID: "455", Participants: []domain.Participant{{Name: "Cleveland Browns", Abbreviation: "CLE"}, {Name: "Jacksonville Jaguars", Abbreviation: "JAX"}}}}
	markets := []domain.CanonicalMarket{{ExchangeTicker: "KXNFL", Title: "Cleveland Browns at Jacksonville Jaguars"}}
	matched := Match(events, markets)
	if matched[0].EventID != "455" || matched[0].MappingStatus != "accepted" {
		t.Fatalf("unexpected match %+v", matched[0])
	}
}

func TestCandidatesRequireEvidenceAndNearbyTime(t *testing.T) {
	start := time.Date(2026, 8, 31, 18, 0, 0, 0, time.UTC)
	events := []domain.CanonicalEvent{
		{ID: "near", StartTime: start, Participants: []domain.Participant{{Rotation: "451", Name: "Massachusetts"}, {Rotation: "452", Name: "Rutgers"}}},
		{ID: "far", StartTime: start.Add(48 * time.Hour), Participants: []domain.Participant{{Name: "Massachusetts"}, {Name: "Rutgers"}}},
		{ID: "other", StartTime: start, Participants: []domain.Participant{{Name: "Duke"}, {Name: "Virginia"}}},
	}
	candidates := Candidates(events, domain.CanonicalMarket{Title: "UMass vs Rutgers", OccurrenceTime: start})
	if len(candidates) != 1 || candidates[0].EventID != "near" || candidates[0].Participants[0].Rotation != "451" {
		t.Fatalf("unexpected review candidates %+v", candidates)
	}
}

func TestMatchUsesAuthoritativeTwoTeamTitle(t *testing.T) {
	events := []domain.CanonicalEvent{
		{ID: "141", Participants: []domain.Participant{{Name: "Massachusetts"}, {Name: "Rutgers"}}},
		{ID: "143", Participants: []domain.Participant{{Name: "Wake Forest"}, {Name: "Rutgers"}}},
	}
	markets := []domain.CanonicalMarket{{ExchangeTicker: "KXNCAAFGAME-26SEP03MASSRUTG-RUTG", Title: "UMass vs Rutgers", Outcome: "Rutgers"}}
	matched := Match(events, markets)
	if matched[0].EventID != "141" || matched[0].MappingStatus != "accepted" {
		t.Fatalf("unexpected match %+v", matched[0])
	}
}

func TestParticipantIndexUnderstandsOutcomeText(t *testing.T) {
	event := domain.CanonicalEvent{Participants: []domain.Participant{{Name: "Massachusetts"}, {Name: "Rutgers"}}}
	if got := ParticipantIndex(event, "Rutgers wins by over 24.5 points"); got != 1 {
		t.Fatalf("got participant %d", got)
	}
	if got := ParticipantIndex(event, "UMass wins"); got != 0 {
		t.Fatalf("got participant %d", got)
	}
	abbreviated := domain.CanonicalEvent{Participants: []domain.Participant{{Name: "Toledo"}, {Name: "Michigan State"}}}
	if got := ParticipantIndex(abbreviated, "Michigan St. wins by over 20.5 points"); got != 1 {
		t.Fatalf("abbreviated state outcome got participant %d", got)
	}
	fresno := domain.CanonicalEvent{Participants: []domain.Participant{{Name: "Fresno State"}, {Name: "USC"}}}
	if got := ParticipantIndex(fresno, "Fresno St. wins by over 35.5 points"); got != 0 {
		t.Fatalf("abbreviated Fresno outcome got participant %d", got)
	}
	if got := ParticipantIndex(fresno, "USC wins by over 21.5 points"); got != 1 {
		t.Fatalf("short school acronym outcome got participant %d", got)
	}
}

func TestMatchRejectsAmbiguousDuplicateMatchups(t *testing.T) {
	events := []domain.CanonicalEvent{
		{ID: "1", Participants: []domain.Participant{{Name: "Massachusetts"}, {Name: "Rutgers"}}},
		{ID: "2", Participants: []domain.Participant{{Name: "Massachusetts"}, {Name: "Rutgers"}}},
	}
	matched := Match(events, []domain.CanonicalMarket{{Title: "UMass vs Rutgers"}})
	if matched[0].MappingStatus != "review" || matched[0].EventID != "" {
		t.Fatalf("ambiguous matchup should require review: %+v", matched[0])
	}
}

func TestMatchUsesClearlyCloserOccurrenceForDailyBaseballSeries(t *testing.T) {
	occurrence := time.Date(2026, 9, 2, 4, 40, 0, 0, time.UTC)
	events := []domain.CanonicalEvent{
		{ID: "959", StartTime: occurrence.Add(-3 * time.Hour), Participants: []domain.Participant{{Name: "Philadelphia Phillies"}, {Name: "Arizona Diamondbacks"}}},
		{ID: "905", StartTime: occurrence.Add(15 * time.Hour), Participants: []domain.Participant{{Name: "Philadelphia Phillies"}, {Name: "Arizona Diamondbacks"}}},
	}
	matched := Match(events, []domain.CanonicalMarket{{Title: "Philadelphia vs Arizona", OccurrenceTime: occurrence}})
	if matched[0].EventID != "959" || matched[0].MappingStatus != "accepted" {
		t.Fatalf("clearly closer daily game was not selected: %+v", matched[0])
	}
}

func TestMatchKeepsCloseDoubleheaderInReview(t *testing.T) {
	occurrence := time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC)
	events := []domain.CanonicalEvent{
		{ID: "game-1", StartTime: occurrence.Add(-2 * time.Hour), Participants: []domain.Participant{{Name: "Chicago Cubs"}, {Name: "St. Louis Cardinals"}}},
		{ID: "game-2", StartTime: occurrence.Add(2 * time.Hour), Participants: []domain.Participant{{Name: "Chicago Cubs"}, {Name: "St. Louis Cardinals"}}},
	}
	matched := Match(events, []domain.CanonicalMarket{{Title: "Chicago C vs St. Louis", OccurrenceTime: occurrence}})
	if matched[0].EventID != "" || matched[0].MappingStatus != "review" {
		t.Fatalf("close doubleheader should remain ambiguous: %+v", matched[0])
	}
}

func TestMatchHandlesCommonScheduleAndKalshiTeamAliases(t *testing.T) {
	tests := []struct {
		away  string
		home  string
		title string
	}{
		{away: "UL Monroe", home: "Mississippi State", title: "Louisiana-Monroe vs Mississippi St."},
		{away: "UNLV", home: "Hawaii", title: "UNLV vs Hawai'i"},
		{away: "Miami Florida", home: "Stanford", title: "Miami (FL) vs Stanford: Spread"},
		{away: "Fresno State", home: "USC", title: "Fresno St. vs USC: Spread"},
		{away: "Toledo", home: "Michigan State", title: "Toledo vs Michigan St.: Spread"},
	}
	for _, tt := range tests {
		events := []domain.CanonicalEvent{{ID: "game", Participants: []domain.Participant{{Name: tt.away}, {Name: tt.home}}}}
		matched := Match(events, []domain.CanonicalMarket{{Title: tt.title}})
		if matched[0].EventID != "game" || matched[0].MappingStatus != "accepted" {
			t.Fatalf("%s did not match %s at %s: %+v", tt.title, tt.away, tt.home, matched[0])
		}
	}
}
