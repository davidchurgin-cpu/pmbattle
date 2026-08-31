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
