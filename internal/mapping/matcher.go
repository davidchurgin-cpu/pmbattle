package mapping

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

var wordRE = regexp.MustCompile(`[a-z0-9]+`)

func Match(events []domain.CanonicalEvent, markets []domain.CanonicalMarket) []domain.CanonicalMarket {
	result := make([]domain.CanonicalMarket, len(markets))
	copy(result, markets)
	for i := range result {
		bestID, bestScore := "", 0
		marketWords := tokens(result[i].Title + " " + result[i].Subtitle)
		for _, event := range events {
			score := 0
			for _, participant := range event.Participants {
				for word := range tokens(participant.Name + " " + participant.Abbreviation) {
					if marketWords[word] {
						score++
					}
				}
			}
			if score > bestScore {
				bestScore = score
				bestID = event.ID
			}
		}
		result[i].EventID = bestID
		result[i].MappingConfidence = bestScore * 25
		if result[i].MappingConfidence > 100 {
			result[i].MappingConfidence = 100
		}
		if result[i].MappingConfidence >= 75 {
			result[i].MappingStatus = "accepted"
		} else {
			result[i].MappingStatus = "review"
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].MappingConfidence > result[j].MappingConfidence })
	return result
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
