package schedule

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

type feed struct {
	Sports []sport `xml:"sport"`
}

type sport struct {
	Name    string   `xml:"name,attr"`
	ID      string   `xml:"sportID,attr"`
	Leagues []league `xml:"league"`
}

type league struct {
	Name  string `xml:"name,attr"`
	ID    string `xml:"leagueID,attr"`
	Games []game `xml:"game"`
}

type game struct {
	Headers []header `xml:"header"`
}

type header struct {
	Events []event `xml:"event"`
}

type event struct {
	Date         string        `xml:"date,attr"`
	ID           string        `xml:"id,attr"`
	UTC          string        `xml:"utc,attr"`
	Participants []participant `xml:"participant"`
	Score        score         `xml:"score"`
}

type participant struct {
	ID   string `xml:"id,attr"`
	Rot  string `xml:"rot,attr"`
	Team string `xml:"team,attr"`
	Abbr string `xml:"abbr,attr"`
}

type score struct {
	Away   string `xml:"away_score,attr"`
	Home   string `xml:"home_score,attr"`
	Final  bool   `xml:"is_final,attr"`
	Period string `xml:"period,attr"`
	Timer  string `xml:"timer,attr"`
}

func Parse(r io.Reader) ([]domain.CanonicalEvent, error) {
	var raw feed
	decoder := xml.NewDecoder(r)
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode schedule: %w", err)
	}
	var result []domain.CanonicalEvent
	for _, s := range raw.Sports {
		for _, l := range s.Leagues {
			for _, g := range l.Games {
				for _, h := range g.Headers {
					for _, e := range h.Events {
						start, err := time.Parse(time.RFC3339, e.UTC)
						if err != nil {
							continue
						}
						participants := make([]domain.Participant, 0, len(e.Participants))
						for _, p := range e.Participants {
							participants = append(participants, domain.Participant{ID: p.ID, Rotation: p.Rot, Name: strings.TrimSpace(p.Team), Abbreviation: strings.TrimSpace(p.Abbr)})
						}
						status := "scheduled"
						if e.Score.Final {
							status = "final"
						} else if e.Score.Period != "" || e.Score.Timer != "" {
							status = "live"
						}
						result = append(result, domain.CanonicalEvent{ID: e.ID, SportID: s.ID, Sport: s.Name, LeagueID: l.ID, League: l.Name, StartTime: start.UTC(), Status: status, Period: e.Score.Period, Timer: e.Score.Timer, IsFinal: e.Score.Final, AwayScore: e.Score.Away, HomeScore: e.Score.Home, Participants: participants})
					}
				}
			}
		}
	}
	return result, nil
}
