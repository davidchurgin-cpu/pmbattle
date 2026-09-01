// Package routing plans one cash-at-risk target across exchange order-book
// levels. It is deliberately independent from exchange execution: adapters
// provide fee-included capacity, while a later coordinator owns child orders
// and reacts to fills.
package routing

import (
	"errors"
	"sort"
	"strings"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
)

var ErrInvalidRequest = errors.New("invalid routing request")

type Request struct {
	CashRiskTarget    domain.Money `json:"cashRiskTarget"`
	PriceCapMoneyline int64        `json:"priceCapMoneyline"`
}

// Candidate is one executable price level. LiquidityRisk is the maximum
// fee-included cash cost available at this level. AvailableCash is the
// exchange-reported cash currently available for trading; CommittedRisk is a
// PMBattle promise not yet reflected in that balance, such as hidden iceberg
// slices belonging to another parent.
type Candidate struct {
	Exchange       string       `json:"exchange"`
	Ticker         string       `json:"ticker"`
	Side           string       `json:"side"`
	RawPrice       domain.Money `json:"rawPrice"`
	AllInMoneyline int64        `json:"allInMoneyline"`
	LiquidityRisk  domain.Money `json:"liquidityRisk"`
	AvailableCash  domain.Money `json:"availableCash"`
	CommittedRisk  domain.Money `json:"committedRisk"`
	Stale          bool         `json:"stale"`
}

type Allocation struct {
	Exchange       string       `json:"exchange"`
	Ticker         string       `json:"ticker"`
	Side           string       `json:"side"`
	RawPrice       domain.Money `json:"rawPrice"`
	AllInMoneyline int64        `json:"allInMoneyline"`
	CashRisk       domain.Money `json:"cashRisk"`
}

type Rejection struct {
	Exchange string `json:"exchange"`
	Ticker   string `json:"ticker"`
	Reason   string `json:"reason"`
}

type Plan struct {
	CashRiskTarget  domain.Money `json:"cashRiskTarget"`
	AllocatedRisk   domain.Money `json:"allocatedRisk"`
	UnallocatedRisk domain.Money `json:"unallocatedRisk"`
	Complete        bool         `json:"complete"`
	Allocations     []Allocation `json:"allocations"`
	Rejected        []Rejection  `json:"rejected,omitempty"`
}

type rankedCandidate struct {
	candidate   Candidate
	probability domain.Money
	venueKey    string
}

// Build returns a deterministic best-price-first allocation. Venue cash is
// shared across every level from that exchange, so multiple levels cannot
// each spend the same bankroll. An incomplete plan is a normal result and
// reports the exact risk that could not safely be allocated.
func Build(request Request, candidates []Candidate) (Plan, error) {
	if request.CashRiskTarget <= 0 {
		return Plan{}, ErrInvalidRequest
	}
	capProbability, err := pricing.ProbabilityFromMoneyline(request.PriceCapMoneyline)
	if err != nil {
		return Plan{}, ErrInvalidRequest
	}

	plan := Plan{CashRiskTarget: request.CashRiskTarget, UnallocatedRisk: request.CashRiskTarget}
	venueCash := make(map[string]domain.Money)
	ranked := make([]rankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Exchange = strings.TrimSpace(candidate.Exchange)
		candidate.Ticker = strings.TrimSpace(candidate.Ticker)
		candidate.Side = strings.ToLower(strings.TrimSpace(candidate.Side))
		venueKey := strings.ToLower(candidate.Exchange)
		netCash := candidate.AvailableCash - candidate.CommittedRisk
		if netCash < 0 {
			netCash = 0
		}
		if prior, ok := venueCash[venueKey]; !ok || netCash < prior {
			venueCash[venueKey] = netCash
		}

		reason := ""
		switch {
		case candidate.Exchange == "" || candidate.Ticker == "" || candidate.Side != "yes" && candidate.Side != "no":
			reason = "invalid_candidate"
		case candidate.Stale:
			reason = "stale"
		case candidate.LiquidityRisk <= 0:
			reason = "no_liquidity"
		case netCash <= 0:
			reason = "no_available_cash"
		}
		probability, probabilityErr := pricing.ProbabilityFromMoneyline(candidate.AllInMoneyline)
		if reason == "" && probabilityErr != nil {
			reason = "invalid_all_in_price"
		}
		if reason == "" && probability > capProbability {
			reason = "price_cap"
		}
		if reason != "" {
			plan.Rejected = append(plan.Rejected, Rejection{Exchange: candidate.Exchange, Ticker: candidate.Ticker, Reason: reason})
			continue
		}
		ranked = append(ranked, rankedCandidate{candidate: candidate, probability: probability, venueKey: venueKey})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].probability != ranked[j].probability {
			return ranked[i].probability < ranked[j].probability
		}
		left, right := ranked[i].candidate, ranked[j].candidate
		if !strings.EqualFold(left.Exchange, right.Exchange) {
			return strings.ToLower(left.Exchange) < strings.ToLower(right.Exchange)
		}
		if left.Ticker != right.Ticker {
			return left.Ticker < right.Ticker
		}
		return left.RawPrice < right.RawPrice
	})

	remaining := request.CashRiskTarget
	for _, item := range ranked {
		if remaining == 0 {
			break
		}
		capacity := minMoney(item.candidate.LiquidityRisk, venueCash[item.venueKey], remaining)
		if capacity <= 0 {
			continue
		}
		plan.Allocations = append(plan.Allocations, Allocation{
			Exchange: item.candidate.Exchange, Ticker: item.candidate.Ticker, Side: item.candidate.Side,
			RawPrice: item.candidate.RawPrice, AllInMoneyline: item.candidate.AllInMoneyline, CashRisk: capacity,
		})
		venueCash[item.venueKey] -= capacity
		remaining -= capacity
	}
	plan.AllocatedRisk = request.CashRiskTarget - remaining
	plan.UnallocatedRisk = remaining
	plan.Complete = remaining == 0
	return plan, nil
}

func minMoney(values ...domain.Money) domain.Money {
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}
