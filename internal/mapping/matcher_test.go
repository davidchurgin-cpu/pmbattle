package mapping

import (
	"testing"

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
