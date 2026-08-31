package pricing

import (
	"errors"
	"math"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

const (
	generalTakerRate int64 = 700 // 0.07 in 1/10000 units
	generalMakerRate int64 = 175 // 0.0175 in 1/10000 units
)

func MoneylineFromProbability(probability domain.Money) (int64, error) {
	if probability <= 0 || probability >= domain.Dollar {
		return 0, errors.New("probability must be between zero and one")
	}
	if probability == domain.Dollar/2 {
		return 100, nil
	}
	if probability < domain.Dollar/2 {
		return int64(math.Round(float64(100*(domain.Dollar-probability)) / float64(probability))), nil
	}
	return -int64(math.Round(float64(100*probability) / float64(domain.Dollar-probability))), nil
}

// KalshiFee implements the general binary-contract fee formula using integer math.
// Price and quantity are fixed-point values with four decimal places. The result
// is rounded up to the nearest centicent ($0.0001), matching current Kalshi rules.
func KalshiFee(price, quantity domain.Money, maker bool) domain.Money {
	if price <= 0 || price >= domain.Dollar || quantity <= 0 {
		return 0
	}
	rate := generalTakerRate
	if maker {
		rate = generalMakerRate
	}
	// rate * contracts * p * (1-p), preserving four decimal places.
	numerator := rate * int64(quantity) * int64(price) * int64(domain.Dollar-price)
	denominator := int64(domain.Dollar) * int64(domain.Dollar) * int64(domain.Dollar)
	return domain.Money((numerator + denominator - 1) / denominator)
}

func Quote(price, quantity domain.Money, maker bool) (domain.PriceQuote, error) {
	rawML, err := MoneylineFromProbability(price)
	if err != nil {
		return domain.PriceQuote{}, err
	}
	makerFee := KalshiFee(price, quantity, true)
	takerFee := KalshiFee(price, quantity, false)
	fee := takerFee
	if maker {
		fee = makerFee
	}
	cost := domain.Money((int64(price)*int64(quantity) + int64(domain.Dollar) - 1) / int64(domain.Dollar))
	allIn := cost + fee
	payout := quantity
	if payout <= 0 || allIn >= payout {
		return domain.PriceQuote{}, errors.New("invalid all-in quote")
	}
	effectiveProbability := domain.Money((int64(allIn)*int64(domain.Dollar) + int64(payout) - 1) / int64(payout))
	allInML, err := MoneylineFromProbability(effectiveProbability)
	if err != nil {
		return domain.PriceQuote{}, err
	}
	return domain.PriceQuote{RawPrice: price, MakerFee: makerFee, TakerFee: takerFee, AllInCost: allIn, RawMoneyline: rawML, AllInMoneyline: allInML, AvailableQuantity: quantity}, nil
}
