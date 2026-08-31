package domain

import "time"

type Money int64 // fixed-point ten-thousandths of a dollar

const Dollar Money = 10_000

type Participant struct {
	ID           string `json:"id"`
	Rotation     string `json:"rotation"`
	Name         string `json:"name"`
	Abbreviation string `json:"abbreviation"`
}

type CanonicalEvent struct {
	ID           string        `json:"id"`
	SportID      string        `json:"sportId"`
	Sport        string        `json:"sport"`
	LeagueID     string        `json:"leagueId"`
	League       string        `json:"league"`
	StartTime    time.Time     `json:"startTime"`
	Status       string        `json:"status"`
	Period       string        `json:"period,omitempty"`
	Timer        string        `json:"timer,omitempty"`
	IsFinal      bool          `json:"isFinal"`
	AwayScore    string        `json:"awayScore,omitempty"`
	HomeScore    string        `json:"homeScore,omitempty"`
	Participants []Participant `json:"participants"`
	Markets      []MarketView  `json:"markets,omitempty"`
}

type MarketType string

const (
	MarketMoneyline MarketType = "moneyline"
	MarketSpread    MarketType = "spread"
	MarketTotal     MarketType = "total"
)

type CanonicalMarket struct {
	ID                string     `json:"id"`
	EventID           string     `json:"eventId"`
	Exchange          string     `json:"exchange"`
	ExchangeTicker    string     `json:"exchangeTicker"`
	Type              MarketType `json:"type"`
	Outcome           string     `json:"outcome"`
	Line              string     `json:"line,omitempty"`
	Title             string     `json:"title,omitempty"`
	Subtitle          string     `json:"subtitle,omitempty"`
	CloseTime         time.Time  `json:"closeTime,omitempty"`
	OccurrenceTime    time.Time  `json:"occurrenceTime,omitempty"`
	YesBid            Money      `json:"yesBid,omitempty"`
	YesAsk            Money      `json:"yesAsk,omitempty"`
	YesBidSize        Money      `json:"yesBidSize,omitempty"`
	YesAskSize        Money      `json:"yesAskSize,omitempty"`
	MappingConfidence int        `json:"mappingConfidence"`
	MappingStatus     string     `json:"mappingStatus"`
}

type PriceQuote struct {
	Exchange          string `json:"exchange"`
	Ticker            string `json:"ticker"`
	Outcome           string `json:"outcome"`
	Side              string `json:"side"`
	RawPrice          Money  `json:"rawPrice"`
	MakerFee          Money  `json:"makerFee"`
	TakerFee          Money  `json:"takerFee"`
	AllInCost         Money  `json:"allInCost"`
	RawMoneyline      int64  `json:"rawMoneyline"`
	AllInMoneyline    int64  `json:"allInMoneyline"`
	AvailableQuantity Money  `json:"availableQuantity"`
}

type MarketView struct {
	Type    MarketType     `json:"type"`
	Line    string         `json:"line,omitempty"`
	Away    *PriceQuote    `json:"away,omitempty"`
	Home    *PriceQuote    `json:"home,omitempty"`
	Over    *PriceQuote    `json:"over,omitempty"`
	Under   *PriceQuote    `json:"under,omitempty"`
	Options []MarketOption `json:"options,omitempty"`
	Status  string         `json:"status"`
}

type MarketOption struct {
	Line  string      `json:"line"`
	Away  *PriceQuote `json:"away,omitempty"`
	Home  *PriceQuote `json:"home,omitempty"`
	Over  *PriceQuote `json:"over,omitempty"`
	Under *PriceQuote `json:"under,omitempty"`
}

type BookLevel struct {
	Price    Money `json:"price"`
	Quantity Money `json:"quantity"`
}

type OrderBook struct {
	Ticker    string      `json:"ticker"`
	Sequence  int64       `json:"sequence"`
	UpdatedAt time.Time   `json:"updatedAt"`
	Stale     bool        `json:"stale"`
	Yes       []BookLevel `json:"yes"`
	No        []BookLevel `json:"no"`
}

type Fill struct {
	ID             string    `json:"id"`
	Exchange       string    `json:"exchange"`
	Ticker         string    `json:"ticker"`
	EventID        string    `json:"eventId,omitempty"`
	Rotation       string    `json:"rotation,omitempty"`
	Team           string    `json:"team,omitempty"`
	Market         string    `json:"market"`
	Side           string    `json:"side"`
	Quantity       Money     `json:"quantity"`
	RawPrice       Money     `json:"rawPrice"`
	AllInMoneyline int64     `json:"allInMoneyline"`
	Fee            Money     `json:"fee"`
	CashRisk       Money     `json:"cashRisk"`
	CreatedAt      time.Time `json:"createdAt"`
}

type Order struct {
	ID             string    `json:"id"`
	Exchange       string    `json:"exchange"`
	Ticker         string    `json:"ticker"`
	Rotation       string    `json:"rotation,omitempty"`
	Market         string    `json:"market"`
	Side           string    `json:"side"`
	Status         string    `json:"status"`
	Quantity       Money     `json:"quantity"`
	FilledQuantity Money     `json:"filledQuantity"`
	LimitPrice     Money     `json:"limitPrice"`
	CashRisk       Money     `json:"cashRisk"`
	CreatedAt      time.Time `json:"createdAt"`
}

type ParentOrder struct {
	ID                string    `json:"id"`
	Exchange          string    `json:"exchange"`
	EventID           string    `json:"eventId"`
	Ticker            string    `json:"ticker"`
	Rotation          string    `json:"rotation,omitempty"`
	Outcome           string    `json:"outcome"`
	Market            string    `json:"market"`
	Side              string    `json:"side"`
	Strategy          string    `json:"strategy"`
	Policy            string    `json:"policy"`
	Status            string    `json:"status"`
	CashRiskTarget    Money     `json:"cashRiskTarget"`
	ReservedRisk      Money     `json:"reservedRisk"`
	FilledRisk        Money     `json:"filledRisk"`
	RemainingRisk     Money     `json:"remainingRisk"`
	PriceCapMoneyline int64     `json:"priceCapMoneyline"`
	LimitPrice        Money     `json:"limitPrice"`
	Quantity          Money     `json:"quantity"`
	ChildOrderIDs     []string  `json:"childOrderIds"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type Position struct {
	Exchange      string `json:"exchange"`
	Ticker        string `json:"ticker"`
	Rotation      string `json:"rotation,omitempty"`
	Market        string `json:"market"`
	Quantity      Money  `json:"quantity"`
	CashRisk      Money  `json:"cashRisk"`
	AveragePrice  Money  `json:"averagePrice"`
	CurrentPrice  Money  `json:"currentPrice"`
	UnrealizedPnL Money  `json:"unrealizedPnl"`
}

type Health struct {
	Status          string    `json:"status"`
	Mode            string    `json:"mode"`
	ScheduleUpdated time.Time `json:"scheduleUpdated"`
	ExchangeState   string    `json:"exchangeState"`
	LatencyMS       int64     `json:"latencyMs"`
	TradingEnabled  bool      `json:"tradingEnabled"`
}

type Preferences struct {
	// Nil means the user has not configured this setting yet, so all sports are enabled.
	// A non-nil empty slice intentionally disables every sport.
	EnabledSports     []string `json:"enabledSports"`
	ExcludeAddedGames bool     `json:"excludeAddedGames"`
}

type SportOption struct {
	Name           string `json:"name"`
	EventCount     int    `json:"eventCount"`
	AddedGameCount int    `json:"addedGameCount"`
	Enabled        bool   `json:"enabled"`
}

type Settings struct {
	Preferences     Preferences   `json:"preferences"`
	AvailableSports []SportOption `json:"availableSports"`
}

type Snapshot struct {
	Events       []CanonicalEvent `json:"events"`
	ParentOrders []ParentOrder    `json:"parentOrders"`
	Orders       []Order          `json:"orders"`
	Positions    []Position       `json:"positions"`
	Fills        []Fill           `json:"fills"`
	Health       Health           `json:"health"`
	Bankroll     Money            `json:"bankroll"`
	AtRisk       Money            `json:"atRisk"`
	Settings     Settings         `json:"settings"`
}

type StreamEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type OrderBookDelta struct {
	Ticker   string `json:"ticker"`
	Sequence int64  `json:"sequence"`
	Side     string `json:"side"`
	Price    Money  `json:"price"`
	Delta    Money  `json:"delta"`
}
