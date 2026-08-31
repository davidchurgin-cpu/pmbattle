package schedule

import (
	"strings"
	"testing"
	"time"
)

const fixture = `<?xml version="1.0"?><odds><sport name="Football" sportID="1"><league leagueID="1" name="NFL"><game><header><event id="455" utc="2026-09-13T17:00:00Z"><participant abbr="CLE" id="21" rot="455" team="Cleveland Browns"/><participant abbr="JAX" id="28" rot="456" team="Jacksonville Jaguars"/><score away_score="7" home_score="10" is_final="false" period="Q2" timer="03:20"/></event></header></game></league></sport></odds>`

func TestParse(t *testing.T) {
	events, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	event := events[0]
	if event.ID != "455" || event.League != "NFL" || event.Status != "live" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Participants[0].Rotation != "455" || event.Participants[1].Name != "Jacksonville Jaguars" {
		t.Fatalf("unexpected participants: %+v", event.Participants)
	}
	if !event.StartTime.Equal(time.Date(2026, 9, 13, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected start %s", event.StartTime)
	}
}

func TestMalformedXML(t *testing.T) {
	if _, err := Parse(strings.NewReader(`<odds><sport>`)); err == nil {
		t.Fatal("expected malformed XML error")
	}
}
