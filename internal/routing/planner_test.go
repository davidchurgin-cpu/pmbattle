package routing

import (
	"errors"
	"testing"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

func TestBuildSplitsFiveThousandAcrossThreeTwoThousandVenues(t *testing.T) {
	plan, err := Build(Request{CashRiskTarget: 5_000 * domain.Dollar, PriceCapMoneyline: -150}, []Candidate{
		{Exchange: "Third", Ticker: "C", Side: "yes", AllInMoneyline: -120, LiquidityRisk: 2_000 * domain.Dollar, AvailableCash: 2_000 * domain.Dollar},
		{Exchange: "Best", Ticker: "A", Side: "yes", AllInMoneyline: +105, LiquidityRisk: 2_000 * domain.Dollar, AvailableCash: 2_000 * domain.Dollar},
		{Exchange: "Second", Ticker: "B", Side: "yes", AllInMoneyline: -105, LiquidityRisk: 2_000 * domain.Dollar, AvailableCash: 2_000 * domain.Dollar},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Complete || plan.AllocatedRisk != 5_000*domain.Dollar || plan.UnallocatedRisk != 0 || len(plan.Allocations) != 3 {
		t.Fatalf("unexpected plan %+v", plan)
	}
	if plan.Allocations[0].Exchange != "Best" || plan.Allocations[0].CashRisk != 2_000*domain.Dollar || plan.Allocations[1].Exchange != "Second" || plan.Allocations[1].CashRisk != 2_000*domain.Dollar || plan.Allocations[2].Exchange != "Third" || plan.Allocations[2].CashRisk != 1_000*domain.Dollar {
		t.Fatalf("wrong best-price allocation order %+v", plan.Allocations)
	}
}

func TestBuildSharesOneVenueBalanceAcrossLevels(t *testing.T) {
	plan, err := Build(Request{CashRiskTarget: 4_000 * domain.Dollar, PriceCapMoneyline: -200}, []Candidate{
		{Exchange: "Kalshi", Ticker: "LEVEL-1", Side: "yes", AllInMoneyline: +110, LiquidityRisk: 1_500 * domain.Dollar, AvailableCash: 3_000 * domain.Dollar, CommittedRisk: 500 * domain.Dollar},
		{Exchange: "kalshi", Ticker: "LEVEL-2", Side: "yes", AllInMoneyline: -105, LiquidityRisk: 2_000 * domain.Dollar, AvailableCash: 3_000 * domain.Dollar, CommittedRisk: 500 * domain.Dollar},
		{Exchange: "Other", Ticker: "LEVEL-3", Side: "yes", AllInMoneyline: -110, LiquidityRisk: 2_000 * domain.Dollar, AvailableCash: 2_000 * domain.Dollar},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Complete || len(plan.Allocations) != 3 {
		t.Fatalf("unexpected plan %+v", plan)
	}
	kalshiRisk := domain.Money(0)
	for _, allocation := range plan.Allocations {
		if allocation.Exchange == "Kalshi" || allocation.Exchange == "kalshi" {
			kalshiRisk += allocation.CashRisk
		}
	}
	if kalshiRisk != 2_500*domain.Dollar {
		t.Fatalf("shared venue spent %d, want %d; allocations=%+v", kalshiRisk, 2_500*domain.Dollar, plan.Allocations)
	}
}

func TestBuildRejectsStaleOverCapAndEmptyCandidates(t *testing.T) {
	plan, err := Build(Request{CashRiskTarget: 1_000 * domain.Dollar, PriceCapMoneyline: -110}, []Candidate{
		{Exchange: "Stale", Ticker: "A", Side: "yes", AllInMoneyline: +120, LiquidityRisk: 1_000 * domain.Dollar, AvailableCash: 1_000 * domain.Dollar, Stale: true},
		{Exchange: "Expensive", Ticker: "B", Side: "yes", AllInMoneyline: -150, LiquidityRisk: 1_000 * domain.Dollar, AvailableCash: 1_000 * domain.Dollar},
		{Exchange: "Empty", Ticker: "C", Side: "yes", AllInMoneyline: +110, LiquidityRisk: 0, AvailableCash: 1_000 * domain.Dollar},
		{Exchange: "Good", Ticker: "D", Side: "yes", AllInMoneyline: -105, LiquidityRisk: 600 * domain.Dollar, AvailableCash: 600 * domain.Dollar},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Complete || plan.AllocatedRisk != 600*domain.Dollar || plan.UnallocatedRisk != 400*domain.Dollar || len(plan.Rejected) != 3 {
		t.Fatalf("unexpected bounded plan %+v", plan)
	}
	reasons := map[string]bool{}
	for _, rejection := range plan.Rejected {
		reasons[rejection.Reason] = true
	}
	if !reasons["stale"] || !reasons["price_cap"] || !reasons["no_liquidity"] {
		t.Fatalf("missing rejection reasons %+v", plan.Rejected)
	}
}

func TestBuildValidatesParentRequest(t *testing.T) {
	if _, err := Build(Request{CashRiskTarget: 0, PriceCapMoneyline: -110}, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("zero target error=%v", err)
	}
	if _, err := Build(Request{CashRiskTarget: domain.Dollar, PriceCapMoneyline: -99}, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid cap error=%v", err)
	}
}

func TestBuildIsDeterministicAtEqualPrice(t *testing.T) {
	candidates := []Candidate{
		{Exchange: "Zulu", Ticker: "B", Side: "yes", AllInMoneyline: -105, LiquidityRisk: 100 * domain.Dollar, AvailableCash: 100 * domain.Dollar},
		{Exchange: "Beta", Ticker: "C", Side: "yes", AllInMoneyline: -105, LiquidityRisk: 100 * domain.Dollar, AvailableCash: 100 * domain.Dollar},
		{Exchange: "Alpha", Ticker: "A", Side: "yes", AllInMoneyline: -105, LiquidityRisk: 100 * domain.Dollar, AvailableCash: 100 * domain.Dollar},
	}
	plan, err := Build(Request{CashRiskTarget: 300 * domain.Dollar, PriceCapMoneyline: -110}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Allocations[0].Exchange != "Alpha" || plan.Allocations[1].Exchange != "Beta" || plan.Allocations[2].Exchange != "Zulu" {
		t.Fatalf("nondeterministic allocation order %+v", plan.Allocations)
	}
}
