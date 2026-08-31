package mapping

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

var wordRE = regexp.MustCompile(`[a-z0-9]+`)
var matchupRE = regexp.MustCompile(`(?i)\s+(?:vs\.?|at|v)\s+`)

func Match(events []domain.CanonicalEvent, markets []domain.CanonicalMarket) []domain.CanonicalMarket {
	result := make([]domain.CanonicalMarket, len(markets))
	copy(result, markets)
	for i := range result {
		bestID, bestScore, bestCount := "", 0, 0
		for _, event := range events {
			score := matchupScore(event, result[i].Title)
			if score > 0 && !result[i].OccurrenceTime.IsZero() && !event.StartTime.IsZero() {
				difference := event.StartTime.Sub(result[i].OccurrenceTime)
				if difference < 0 {
					difference = -difference
				}
				if difference > 36*time.Hour {
					score = 0
				}
			}
			if score > bestScore {
				bestScore = score
				bestID = event.ID
				bestCount = 1
			} else if score > 0 && score == bestScore {
				bestCount++
			}
		}
		if bestCount != 1 {
			bestID, bestScore = "", 0
		}
		result[i].EventID = bestID
		result[i].MappingConfidence = bestScore
		if result[i].MappingConfidence >= 75 {
			result[i].MappingStatus = "accepted"
		} else {
			result[i].MappingStatus = "review"
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].MappingConfidence > result[j].MappingConfidence })
	return result
}

func matchupScore(event domain.CanonicalEvent, title string) int {
	if len(event.Participants) < 2 {
		return 0
	}
	matchup := strings.TrimSpace(strings.SplitN(title, ":", 2)[0])
	sides := matchupRE.Split(matchup, 2)
	if len(sides) != 2 {
		return 0
	}
	directA := teamSimilarity(event.Participants[0], sides[0])
	directB := teamSimilarity(event.Participants[1], sides[1])
	reverseA := teamSimilarity(event.Participants[0], sides[1])
	reverseB := teamSimilarity(event.Participants[1], sides[0])
	direct, reverse := pairScore(directA, directB), pairScore(reverseA, reverseB)
	if reverse > direct {
		return reverse
	}
	return direct
}

func pairScore(first, second int) int {
	if first < 70 || second < 70 {
		return 0
	}
	return (first + second) / 2
}

// ParticipantIndex resolves a Kalshi outcome label to an event participant.
// It returns -1 unless one participant is a clear, conservative match.
func ParticipantIndex(event domain.CanonicalEvent, value string) int {
	bestIndex, bestScore, secondScore := -1, 0, 0
	for i, participant := range event.Participants {
		score := teamSimilarity(participant, value)
		if score > bestScore {
			bestIndex, secondScore, bestScore = i, bestScore, score
		} else if score > secondScore {
			secondScore = score
		}
	}
	if bestScore < 70 || bestScore-secondScore < 10 {
		return -1
	}
	return bestIndex
}

func teamSimilarity(participant domain.Participant, candidate string) int {
	names := []string{participant.Name, participant.Abbreviation}
	best := 0
	for _, name := range names {
		left, right := canonicalTeam(name), canonicalTeam(candidate)
		if left == "" || right == "" {
			continue
		}
		score := 0
		switch {
		case left == right:
			score = 100
		case len(left) >= 4 && strings.Contains(right, left):
			score = 90
		case len(right) >= 4 && strings.Contains(left, right):
			score = 85
		default:
			leftWords, rightWords := tokens(left), tokens(right)
			matched := 0
			for word := range leftWords {
				if len(word) >= 4 && rightWords[word] {
					matched++
				}
			}
			if matched > 0 && matched == len(leftWords) {
				score = 80
			}
		}
		if score > best {
			best = score
		}
	}
	return best
}

func canonicalTeam(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	words := wordRE.FindAllString(value, -1)
	value = " " + strings.Join(words, " ") + " "
	aliases := map[string]string{
		"umass": "massachusetts", "uconn": "connecticut", "ole miss": "mississippi",
		"lsu": "louisiana state", "ucf": "central florida", "smu": "southern methodist",
		"tcu": "texas christian", "byu": "brigham young", "nc state": "north carolina state",
	}
	for alias, replacement := range aliases {
		value = strings.ReplaceAll(value, " "+alias+" ", " "+replacement+" ")
	}
	return strings.TrimSpace(value)
}

func tokens(value string) map[string]bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, value)
	out := map[string]bool{}
	for _, w := range wordRE.FindAllString(normalized, -1) {
		if len(w) > 1 {
			out[w] = true
		}
	}
	return out
}
