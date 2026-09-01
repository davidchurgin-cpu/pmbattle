package app

import (
	"context"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

// Readable names for account rows.
//
// Kalshi identifies a market by a ticker such as KXNCAAFTOTAL-26SEP05ECUALA-53,
// which means nothing at a glance. Every position, order, fill, and settlement
// therefore gets a Game ("#301 Clemson at #302 LSU") and an Outcome
// ("Over 52.5") from the best source available, in this order:
//
//  1. the live schedule, when the ticker maps to a game on the board;
//  2. a stored market label from Kalshi's catalog or a one-off lookup;
//  3. the ticker itself, decoded only as far as it can be trusted.
//
// Tier 3 never invents a number. A ticker's strike is not always the line the
// board shows (a 52.5 total is ticker strike 53), so guessing there would put
// a wrong figure next to real money.

// maxLabelLookupsPerCycle bounds the read-only market lookups one account
// reconcile may issue for tickers it has never seen.
const maxLabelLookupsPerCycle = 40

// labelRetryAfter is how long a failed lookup is remembered before retrying.
const labelRetryAfter = time.Hour

type description struct {
	Game     string
	Outcome  string
	Market   string
	Rotation string
	EventID  string
}

// describeLocked resolves a ticker and contract side. Callers hold s.mu.
func (s *Service) describeLocked(ticker, side string) description {
	side = strings.ToLower(strings.TrimSpace(side))
	if event, market, quote, ok := findQuoteForSide(s.snapshot.Events, ticker, side); ok {
		return description{
			Game:     gameName(event),
			Outcome:  outcomeName(market, quote),
			Market:   marketName(market),
			Rotation: rotationForOutcome(event, quote.Outcome),
			EventID:  event.ID,
		}
	}
	if label, ok := s.labels[ticker]; ok {
		return describeFromLabel(label, side)
	}
	return describeFromTicker(ticker, side)
}

func gameName(event domain.CanonicalEvent) string {
	parts := make([]string, 0, len(event.Participants))
	for _, participant := range event.Participants {
		name := strings.TrimSpace(participant.Name)
		if name == "" {
			continue
		}
		if participant.Rotation != "" {
			name = "#" + participant.Rotation + " " + name
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, " at ")
}

func outcomeName(market domain.MarketView, quote domain.PriceQuote) string {
	switch market.Type {
	case domain.MarketTotal:
		return strings.TrimSpace(quote.Outcome + " " + market.Line)
	case domain.MarketSpread:
		line := market.Line
		// MarketView.Line always describes the away side, so the home side
		// takes the mirrored number.
		if market.Home != nil && quote.Ticker == market.Home.Ticker && quote.Side == market.Home.Side {
			line = mirrorLine(line)
		}
		return strings.TrimSpace(quote.Outcome + " " + line)
	default:
		return quote.Outcome
	}
}

func mirrorLine(line string) string {
	switch {
	case line == "":
		return ""
	case strings.HasPrefix(line, "-"):
		return "+" + line[1:]
	case strings.HasPrefix(line, "+"):
		return "-" + line[1:]
	default:
		return "-" + line
	}
}

// findQuoteForSide prefers the quote on the requested contract side, because a
// total's Over and Under share one ticker and differ only by side.
func findQuoteForSide(events []domain.CanonicalEvent, ticker, side string) (domain.CanonicalEvent, domain.MarketView, domain.PriceQuote, bool) {
	if strings.TrimSpace(ticker) == "" {
		return domain.CanonicalEvent{}, domain.MarketView{}, domain.PriceQuote{}, false
	}
	var fallbackEvent domain.CanonicalEvent
	var fallbackMarket domain.MarketView
	var fallbackQuote domain.PriceQuote
	found := false
	for _, event := range events {
		for _, market := range event.Markets {
			options := append([]domain.MarketOption{{Line: market.Line, Away: market.Away, Home: market.Home, Over: market.Over, Under: market.Under}}, market.Options...)
			for _, option := range options {
				for _, quote := range []*domain.PriceQuote{option.Away, option.Home, option.Over, option.Under} {
					if quote == nil || quote.Ticker != ticker {
						continue
					}
					view := market
					view.Line = option.Line
					view.Away, view.Home, view.Over, view.Under = option.Away, option.Home, option.Over, option.Under
					quoteSide := quote.Side
					if quoteSide == "" {
						quoteSide = "yes"
					}
					if side == "" || quoteSide == side {
						return event, view, *quote, true
					}
					if !found {
						fallbackEvent, fallbackMarket, fallbackQuote, found = event, view, *quote, true
					}
				}
			}
		}
	}
	if found {
		// Same ticker, opposite contract side: name the complementary outcome.
		if other := oppositeQuote(fallbackMarket, fallbackQuote); other != nil {
			return fallbackEvent, fallbackMarket, *other, true
		}
		fallbackQuote.Outcome = "Not " + fallbackQuote.Outcome
		return fallbackEvent, fallbackMarket, fallbackQuote, true
	}
	return domain.CanonicalEvent{}, domain.MarketView{}, domain.PriceQuote{}, false
}

func oppositeQuote(market domain.MarketView, quote domain.PriceQuote) *domain.PriceQuote {
	for _, candidate := range []*domain.PriceQuote{market.Away, market.Home, market.Over, market.Under} {
		if candidate != nil && candidate.Ticker == quote.Ticker && candidate.Side != quote.Side {
			return candidate
		}
	}
	return nil
}

func describeFromLabel(label domain.MarketLabel, side string) description {
	outcome := strings.TrimSpace(label.YesOutcome)
	if side == "no" {
		if strings.TrimSpace(label.NoOutcome) != "" {
			outcome = strings.TrimSpace(label.NoOutcome)
		} else if outcome != "" {
			outcome = "Not " + outcome
		}
	}
	marketType := label.Type
	if marketType == "" {
		marketType = tickerParts(label.Ticker).marketType
	}
	market := titleCase(string(marketType))
	// The stored line comes from Kalshi's own catalog, so it is safe to show.
	if market != "" && label.Line != "" && marketType != domain.MarketMoneyline {
		market += " " + label.Line
	}
	game := strings.TrimSpace(label.Title)
	if game == "" {
		game = describeFromTicker(label.Ticker, side).Game
	}
	return description{Game: game, Outcome: outcome, Market: market}
}

// tickerPattern splits a Kalshi sportsbook ticker such as
// KXNCAAFTOTAL-26SEP05ECUALA-53 into league, market type, date, and teams.
var tickerPattern = regexp.MustCompile(`^KX([A-Z]+?)(GAME|SPREAD|TOTAL)-(\d{2})([A-Z]{3})(\d{2})([A-Z0-9]+?)(?:-(.+))?$`)

var leagueNames = map[string]string{
	"NFL": "NFL", "NCAAF": "College Football", "CFL": "CFL", "MLB": "MLB", "NPB": "NPB", "KBO": "KBO",
	"NBA": "NBA", "WNBA": "WNBA", "NCAAMB": "College Basketball", "NCAAWB": "Women's College Basketball",
	"NHL": "NHL", "EPL": "Premier League", "LALIGA": "La Liga", "BUNDESLIGA": "Bundesliga",
	"LIGUE1": "Ligue 1", "SERIEA": "Serie A", "MLS": "MLS",
}

type parsedTicker struct {
	league     string
	teams      string
	date       time.Time
	marketType domain.MarketType
	ok         bool
}

func tickerParts(ticker string) parsedTicker {
	match := tickerPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(ticker)))
	if match == nil {
		return parsedTicker{}
	}
	parsed := parsedTicker{league: match[1], teams: match[6], ok: true}
	switch match[2] {
	case "SPREAD":
		parsed.marketType = domain.MarketSpread
	case "TOTAL":
		parsed.marketType = domain.MarketTotal
	default:
		parsed.marketType = domain.MarketMoneyline
	}
	month := match[4][:1] + strings.ToLower(match[4][1:])
	if date, err := time.Parse("06Jan02", match[3]+month+match[5]); err == nil {
		parsed.date = date
	}
	return parsed
}

// describeFromTicker is the last resort. It turns
// KXNCAAFTOTAL-26SEP05ECUALA-53 into "College Football · Sep 5 · ECUALA" and
// "Total", which beats the raw code without inventing a line number.
func describeFromTicker(ticker, side string) description {
	parsed := tickerParts(ticker)
	if !parsed.ok {
		return description{Game: ticker}
	}
	league := leagueNames[parsed.league]
	if league == "" {
		league = parsed.league
	}
	parts := []string{league}
	if !parsed.date.IsZero() {
		parts = append(parts, parsed.date.Format("Jan 2"))
	}
	if parsed.teams != "" {
		parts = append(parts, parsed.teams)
	}
	outcome := "Yes"
	if parsed.marketType == domain.MarketTotal {
		outcome = "Over"
		if side == "no" {
			outcome = "Under"
		}
	} else if side == "no" {
		outcome = "No"
	}
	return description{Game: strings.Join(parts, " · "), Outcome: outcome, Market: titleCase(string(parsed.marketType))}
}

func titleCase(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

// rememberLabels stores catalog identities for every discovered market so rows
// stay readable after the market closes or the game leaves the board.
func (s *Service) rememberLabels(ctx context.Context, markets []domain.CanonicalMarket) {
	labels := make([]domain.MarketLabel, 0, len(markets))
	now := time.Now().UTC()
	for _, market := range markets {
		if market.ExchangeTicker == "" {
			continue
		}
		exchangeName := strings.ToLower(strings.TrimSpace(market.Exchange))
		if exchangeName == "" && s.exchange != nil {
			exchangeName = strings.ToLower(s.exchange.Name())
		}
		labels = append(labels, domain.MarketLabel{
			Exchange: exchangeName, Ticker: market.ExchangeTicker, Title: market.Title,
			YesOutcome: market.Outcome, NoOutcome: market.NoOutcome, Type: market.Type, Line: market.Line, UpdatedAt: now,
		})
	}
	if len(labels) == 0 {
		return
	}
	s.mu.Lock()
	for _, label := range labels {
		s.labels[label.Ticker] = label
	}
	s.mu.Unlock()
	if err := s.store.SaveMarketLabels(ctx, labels); err != nil {
		slog.Warn("persist market labels", "error", err)
	}
}

// lookupMissingLabels asks the exchange, read-only, about tickers that are
// neither on the board nor in the label store, then persists the answers. This
// is what makes settled History rows readable long after the game is over.
func (s *Service) lookupMissingLabels(ctx context.Context, tickers []string) {
	if s.exchange == nil || ctx.Err() != nil {
		return
	}
	s.mu.RLock()
	missing := make([]string, 0)
	seen := make(map[string]bool)
	for _, ticker := range tickers {
		if ticker == "" || seen[ticker] {
			continue
		}
		seen[ticker] = true
		if _, ok := s.labels[ticker]; ok {
			continue
		}
		if _, _, _, ok := findQuoteForSide(s.snapshot.Events, ticker, ""); ok {
			continue
		}
		if failedAt, ok := s.labelFailedAt[ticker]; ok && time.Since(failedAt) < labelRetryAfter {
			continue
		}
		missing = append(missing, ticker)
	}
	s.mu.RUnlock()
	sort.Strings(missing)
	if len(missing) > maxLabelLookupsPerCycle {
		missing = missing[:maxLabelLookupsPerCycle]
	}
	found := make([]domain.MarketLabel, 0, len(missing))
	for _, ticker := range missing {
		if ctx.Err() != nil {
			return
		}
		label, err := s.exchange.DescribeMarket(ctx, ticker)
		if err != nil {
			s.mu.Lock()
			s.labelFailedAt[ticker] = time.Now()
			s.mu.Unlock()
			slog.Debug("market label lookup failed", "ticker", ticker, "error", err)
			continue
		}
		if label.Ticker == "" {
			label.Ticker = ticker
		}
		if label.Exchange == "" {
			label.Exchange = strings.ToLower(s.exchange.Name())
		}
		found = append(found, label)
	}
	if len(found) == 0 {
		return
	}
	s.mu.Lock()
	for _, label := range found {
		s.labels[label.Ticker] = label
		delete(s.labelFailedAt, label.Ticker)
	}
	s.mu.Unlock()
	if err := s.store.SaveMarketLabels(ctx, found); err != nil {
		slog.Warn("persist looked-up market labels", "error", err)
	}
}

func (s *Service) enrichOrderLocked(order *domain.Order) {
	d := s.describeLocked(order.Ticker, order.Side)
	order.Game, order.Outcome, order.Market = d.Game, d.Outcome, d.Market
	if order.Rotation == "" {
		order.Rotation = d.Rotation
	}
	if order.Market == "" {
		order.Market = order.Ticker
	}
}

func (s *Service) enrichFillLocked(fill *domain.Fill) {
	d := s.describeLocked(fill.Ticker, fill.Side)
	if fill.EventID == "" {
		fill.EventID = d.EventID
	}
	if fill.Rotation == "" {
		fill.Rotation = d.Rotation
	}
	fill.Game = d.Game
	if d.Outcome != "" {
		fill.Team = d.Outcome
	}
	if d.Market != "" {
		fill.Market = d.Market
	}
	if fill.Market == "" {
		fill.Market = fill.Ticker
	}
}

// accountTickers lists every ticker shown in the account tabs, so their names
// can be resolved in one pass.
func accountTickers(orders []domain.Order, positions []domain.Position, settlements []domain.Settlement, fills []domain.Fill) []string {
	tickers := make([]string, 0, len(orders)+len(positions)+len(settlements)+len(fills))
	for _, order := range orders {
		tickers = append(tickers, order.Ticker)
	}
	for _, position := range positions {
		tickers = append(tickers, position.Ticker)
	}
	for _, settlement := range settlements {
		tickers = append(tickers, settlement.Ticker)
	}
	for _, fill := range fills {
		tickers = append(tickers, fill.Ticker)
	}
	return tickers
}
