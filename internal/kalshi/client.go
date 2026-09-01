package kalshi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/exchange"
	"github.com/davidchurgin-cpu/pmbattle/internal/fixed"
	"github.com/davidchurgin-cpu/pmbattle/internal/pricing"
	"github.com/gorilla/websocket"
)

type Config struct{ Environment, KeyID, PrivateKeyPath string }

type Client struct {
	cfg            Config
	baseURL, wsURL string
	key            *rsa.PrivateKey
	http           *http.Client
}

func New(cfg Config) (*Client, error) {
	c := &Client{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
	if strings.EqualFold(cfg.Environment, "production") {
		c.baseURL = "https://external-api.kalshi.com/trade-api/v2"
		c.wsURL = "wss://external-api-ws.kalshi.com/trade-api/ws/v2"
	} else {
		c.baseURL = "https://external-api.demo.kalshi.co/trade-api/v2"
		c.wsURL = "wss://external-api-ws.demo.kalshi.co/trade-api/ws/v2"
	}
	if cfg.PrivateKeyPath != "" {
		key, err := loadPrivateKey(cfg.PrivateKeyPath)
		if err != nil {
			return nil, err
		}
		c.key = key
	}
	return c, nil
}

func (c *Client) Name() string { return "kalshi" }

type eventResponse struct {
	Events []event `json:"events"`
	Cursor string  `json:"cursor"`
}
type event struct {
	Ticker       string   `json:"event_ticker"`
	SeriesTicker string   `json:"series_ticker"`
	Title        string   `json:"title"`
	Subtitle     string   `json:"sub_title"`
	Category     string   `json:"category"`
	Markets      []market `json:"markets"`
}
type market struct {
	Ticker      string    `json:"ticker"`
	EventTicker string    `json:"event_ticker"`
	Status      string    `json:"status"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	YesSubTitle string    `json:"yes_sub_title"`
	NoSubTitle  string    `json:"no_sub_title"`
	CloseTime   time.Time `json:"close_time"`
	Occurrence  time.Time `json:"occurrence_datetime"`
	YesBid      string    `json:"yes_bid_dollars"`
	YesAsk      string    `json:"yes_ask_dollars"`
	YesBidSize  string    `json:"yes_bid_size_fp"`
	YesAskSize  string    `json:"yes_ask_size_fp"`
	FloorStrike *float64  `json:"floor_strike"`
}

func (c *Client) ListMarkets(ctx context.Context, scheduleEvents []domain.CanonicalEvent) ([]domain.CanonicalMarket, error) {
	var result []domain.CanonicalMarket
	for _, series := range sportsbookSeries(scheduleEvents) {
		markets, err := c.listSeriesMarkets(ctx, series)
		if err != nil {
			return nil, err
		}
		result = append(result, markets...)
	}
	return result, nil
}

func (c *Client) listSeriesMarkets(ctx context.Context, series string) ([]domain.CanonicalMarket, error) {
	marketType := seriesMarketType(series)
	if marketType == "" {
		return nil, nil
	}
	var result []domain.CanonicalMarket
	cursor := ""
	for page := 0; page < 20; page++ {
		endpoint := c.baseURL + "/events?series_ticker=" + url.QueryEscape(series) + "&status=open&with_nested_markets=true&limit=200"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("kalshi events %s: %s", series, resp.Status)
		}
		var payload eventResponse
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, e := range payload.Events {
			if e.Category != "" && !strings.EqualFold(e.Category, "sports") {
				continue
			}
			for _, m := range e.Markets {
				if m.Status != "" && m.Status != "open" && m.Status != "active" {
					continue
				}
				result = append(result, canonicalMarket(e, m, marketType))
			}
		}
		if payload.Cursor == "" {
			break
		}
		cursor = payload.Cursor
	}
	return result, nil
}

func canonicalMarket(e event, m market, marketType domain.MarketType) domain.CanonicalMarket {
	bid, _ := fixed.Parse(m.YesBid)
	ask, _ := fixed.Parse(m.YesAsk)
	bidSize, _ := fixed.Parse(m.YesBidSize)
	askSize, _ := fixed.Parse(m.YesAskSize)
	line := ""
	if m.FloorStrike != nil {
		line = strconv.FormatFloat(*m.FloorStrike, 'f', -1, 64)
	}
	return domain.CanonicalMarket{
		ID: m.Ticker, Exchange: "kalshi", ExchangeTicker: m.Ticker, Type: marketType,
		Outcome: strings.TrimSpace(m.YesSubTitle), Line: line, Title: strings.TrimSpace(e.Title),
		Subtitle:  strings.TrimSpace(e.Subtitle + " " + m.Title + " " + m.YesSubTitle),
		CloseTime: m.CloseTime, OccurrenceTime: m.Occurrence, YesBid: bid, YesAsk: ask,
		YesBidSize: bidSize, YesAskSize: askSize, MappingStatus: "review",
	}
}

func seriesMarketType(series string) domain.MarketType {
	switch {
	case strings.HasSuffix(series, "SPREAD"):
		return domain.MarketSpread
	case strings.HasSuffix(series, "TOTAL"):
		return domain.MarketTotal
	case strings.HasSuffix(series, "GAME"):
		return domain.MarketMoneyline
	default:
		return ""
	}
}

func sportsbookSeries(events []domain.CanonicalEvent) []string {
	bases := map[string]bool{}
	for _, event := range events {
		league := strings.ToUpper(strings.TrimSpace(event.League))
		switch {
		case league == "NFL":
			bases["KXNFL"] = true
		case strings.Contains(league, "COLLEGE FOOTBALL") || league == "FCS":
			bases["KXNCAAF"] = true
		case strings.Contains(league, "CANADIAN FOOTBALL"):
			bases["KXCFL"] = true
		case league == "MLB" || league == "AMERICAN LEAGUE" || league == "NATIONAL LEAGUE":
			bases["KXMLB"] = true
		case strings.Contains(league, "JAPAN NPB"):
			bases["KXNPB"] = true
		case strings.Contains(league, "KOREA KBO"):
			bases["KXKBO"] = true
		case league == "NBA":
			bases["KXNBA"] = true
		case league == "WNBA":
			bases["KXWNBA"] = true
		case league == "COLLEGE BASKETBALL":
			bases["KXNCAAMB"] = true
		case strings.Contains(league, "WOMEN'S COLLEGE BASKETBALL"):
			bases["KXNCAAWB"] = true
		case league == "NHL":
			bases["KXNHL"] = true
		case league == "ENGLAND PREMIER LEAGUE":
			bases["KXEPL"] = true
		case league == "SPAIN LA LIGA":
			bases["KXLALIGA"] = true
		case league == "GERMAN BUNDESLIGA":
			bases["KXBUNDESLIGA"] = true
		case league == "FRANCE LIGUE 1":
			bases["KXLIGUE1"] = true
		case league == "ITALY SERIE A":
			bases["KXSERIEA"] = true
		case league == "MAJOR LEAGUE":
			bases["KXMLS"] = true
		}
	}
	series := make([]string, 0, len(bases)*3)
	for base := range bases {
		series = append(series, base+"GAME", base+"SPREAD", base+"TOTAL")
	}
	sort.Strings(series)
	return series
}

func (c *Client) Snapshot(ctx context.Context) ([]domain.Order, []domain.Position, []domain.Fill, error) {
	if c.key == nil || c.cfg.KeyID == "" {
		return nil, nil, nil, nil
	}
	orders := make([]domain.Order, 0)
	cursor := ""
	for page := 0; page < 100; page++ {
		path := "/portfolio/orders?status=resting&limit=1000"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var payload struct {
			Orders []rawOrder `json:"orders"`
			Cursor string     `json:"cursor"`
		}
		if err := c.getJSON(ctx, path, &payload); err != nil {
			return nil, nil, nil, err
		}
		for _, raw := range payload.Orders {
			orders = append(orders, normalizeOrder(raw))
		}
		if payload.Cursor == "" {
			break
		}
		cursor = payload.Cursor
	}

	positions := make([]domain.Position, 0)
	cursor = ""
	for page := 0; page < 100; page++ {
		path := "/portfolio/positions?limit=1000&count_filter=position"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var payload struct {
			Positions []rawPosition `json:"market_positions"`
			Cursor    string        `json:"cursor"`
		}
		if err := c.getJSON(ctx, path, &payload); err != nil {
			return nil, nil, nil, err
		}
		for _, raw := range payload.Positions {
			position := normalizePosition(raw)
			if position.Quantity != 0 {
				positions = append(positions, position)
			}
		}
		if payload.Cursor == "" {
			break
		}
		cursor = payload.Cursor
	}
	sort.SliceStable(positions, func(i, j int) bool { return positions[i].UpdatedAt.After(positions[j].UpdatedAt) })
	return orders, positions, nil, nil
}

func (c *Client) Balance(ctx context.Context) (domain.Money, error) {
	if c.key == nil || c.cfg.KeyID == "" {
		return 0, nil
	}
	var payload struct {
		Balance int64 `json:"balance"`
	}
	if err := c.getJSON(ctx, "/portfolio/balance", &payload); err != nil {
		return 0, err
	}
	return domain.Money(payload.Balance * 100), nil
}

func (c *Client) Fills(ctx context.Context, orderIDs []string) ([]domain.Fill, error) {
	if c.key == nil || c.cfg.KeyID == "" || len(orderIDs) == 0 {
		return []domain.Fill{}, nil
	}
	result := make([]domain.Fill, 0)
	seen := make(map[string]bool)
	for _, orderID := range orderIDs {
		if strings.TrimSpace(orderID) == "" {
			continue
		}
		cursor := ""
		for page := 0; page < 100; page++ {
			path := "/portfolio/fills?limit=1000&order_id=" + url.QueryEscape(orderID)
			if cursor != "" {
				path += "&cursor=" + url.QueryEscape(cursor)
			}
			var payload struct {
				Fills  []rawFill `json:"fills"`
				Cursor string    `json:"cursor"`
			}
			if err := c.getJSON(ctx, path, &payload); err != nil {
				return nil, err
			}
			for _, raw := range payload.Fills {
				fill := normalizeFill(raw)
				key := fill.ID
				if key == "" {
					key = fill.OrderID + "|" + fill.CreatedAt.Format(time.RFC3339Nano) + "|" + fixed.Format(fill.Quantity)
				}
				if !seen[key] {
					seen[key] = true
					result = append(result, fill)
				}
			}
			if payload.Cursor == "" {
				break
			}
			cursor = payload.Cursor
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func (c *Client) Settlements(ctx context.Context, since time.Time) ([]domain.Settlement, error) {
	if c.key == nil || c.cfg.KeyID == "" {
		return []domain.Settlement{}, nil
	}
	result := make([]domain.Settlement, 0)
	seen := make(map[string]bool)
	cursor := ""
	for page := 0; page < 100; page++ {
		path := "/portfolio/settlements?limit=1000"
		if !since.IsZero() {
			// Include one overlap second because the API filter is second-granularity.
			path += "&min_ts=" + strconv.FormatInt(since.Add(-time.Second).Unix(), 10)
		}
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var payload struct {
			Settlements []rawSettlement `json:"settlements"`
			Cursor      string          `json:"cursor"`
		}
		if err := c.getJSON(ctx, path, &payload); err != nil {
			return nil, err
		}
		for _, raw := range payload.Settlements {
			settlement := normalizeSettlement(raw)
			if settlement.Ticker != "" && !seen[settlement.Ticker] {
				seen[settlement.Ticker] = true
				result = append(result, settlement)
			}
		}
		if payload.Cursor == "" {
			break
		}
		cursor = payload.Cursor
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].SettledAt.After(result[j].SettledAt) })
	return result, nil
}

func (c *Client) PlaceOrder(ctx context.Context, request exchange.PlaceOrderRequest) (domain.Order, error) {
	if !strings.EqualFold(c.cfg.Environment, "demo") {
		return domain.Order{}, errors.New("kalshi order mutation is locked outside the demo environment")
	}
	if c.key == nil || c.cfg.KeyID == "" {
		return domain.Order{}, errors.New("kalshi order entry requires API credentials")
	}
	if request.OutcomeSide != "yes" && request.OutcomeSide != "no" {
		return domain.Order{}, errors.New("kalshi outcome side must be yes or no")
	}
	price := request.LimitPrice
	bookSide := "bid"
	if request.OutcomeSide == "no" {
		bookSide = "ask"
		price = domain.Dollar - request.LimitPrice
	}
	payload := struct {
		Ticker                  string `json:"ticker"`
		ClientOrderID           string `json:"client_order_id"`
		Side                    string `json:"side"`
		Count                   string `json:"count"`
		Price                   string `json:"price"`
		TimeInForce             string `json:"time_in_force"`
		SelfTradePreventionType string `json:"self_trade_prevention_type"`
		PostOnly                bool   `json:"post_only"`
		CancelOrderOnPause      bool   `json:"cancel_order_on_pause"`
	}{
		Ticker: request.Ticker, ClientOrderID: request.ClientOrderID, Side: bookSide,
		Count: fixed.Format(request.Quantity), Price: fixed.Format(price), TimeInForce: request.TimeInForce,
		SelfTradePreventionType: "taker_at_cross", PostOnly: request.PostOnly, CancelOrderOnPause: true,
	}
	var response struct {
		Order rawOrder `json:"order"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/portfolio/events/orders", payload, &response, http.StatusCreated, http.StatusOK); err != nil {
		return domain.Order{}, err
	}
	order := normalizeOrder(response.Order)
	if order.ID == "" {
		return domain.Order{}, errors.New("kalshi create order response did not include an order id")
	}
	order.Side = request.OutcomeSide
	order.LimitPrice = request.LimitPrice
	if order.Quantity == 0 {
		order.Quantity = request.Quantity
	}
	if order.CashRisk == 0 {
		if quote, err := pricing.Quote(request.LimitPrice, order.Quantity-order.FilledQuantity, false); err == nil {
			order.CashRisk = quote.AllInCost
		}
	}
	return order, nil
}

func (c *Client) AmendOrder(ctx context.Context, request exchange.AmendOrderRequest) (domain.Order, error) {
	if !strings.EqualFold(c.cfg.Environment, "demo") {
		return domain.Order{}, errors.New("kalshi order mutation is locked outside the demo environment")
	}
	if c.key == nil || c.cfg.KeyID == "" {
		return domain.Order{}, errors.New("kalshi order amendment requires API credentials")
	}
	if strings.TrimSpace(request.OrderID) == "" || strings.TrimSpace(request.Ticker) == "" || request.Quantity <= 0 || request.LimitPrice <= 0 || request.LimitPrice >= domain.Dollar {
		return domain.Order{}, errors.New("kalshi amend order request is invalid")
	}
	if request.OutcomeSide != "yes" && request.OutcomeSide != "no" {
		return domain.Order{}, errors.New("kalshi outcome side must be yes or no")
	}
	price := request.LimitPrice
	bookSide := "bid"
	if request.OutcomeSide == "no" {
		bookSide = "ask"
		price = domain.Dollar - request.LimitPrice
	}
	payload := struct {
		Ticker               string `json:"ticker"`
		Side                 string `json:"side"`
		Price                string `json:"price"`
		Count                string `json:"count"`
		ClientOrderID        string `json:"client_order_id,omitempty"`
		UpdatedClientOrderID string `json:"updated_client_order_id,omitempty"`
		ExchangeIndex        int    `json:"exchange_index"`
	}{
		Ticker: request.Ticker, Side: bookSide, Price: fixed.Format(price), Count: fixed.Format(request.Quantity),
		ClientOrderID: request.ClientOrderID, UpdatedClientOrderID: request.UpdatedClientOrderID, ExchangeIndex: -1,
	}
	var response struct {
		OrderID       string `json:"order_id"`
		ClientOrderID string `json:"client_order_id"`
		Remaining     string `json:"remaining_count"`
		TimestampMS   int64  `json:"ts_ms"`
	}
	path := "/portfolio/events/orders/" + url.PathEscape(request.OrderID) + "/amend"
	if err := c.doJSON(ctx, http.MethodPost, path, payload, &response, http.StatusOK); err != nil {
		return domain.Order{}, err
	}
	orderID := response.OrderID
	if orderID == "" {
		orderID = request.OrderID
	}
	status := "resting"
	remaining, _ := fixed.Parse(response.Remaining)
	if remaining == 0 && response.Remaining != "" {
		status = "filled"
	}
	created := time.Now().UTC()
	if response.TimestampMS > 0 {
		created = time.UnixMilli(response.TimestampMS).UTC()
	}
	return domain.Order{
		ID: orderID, Exchange: "Kalshi", Ticker: request.Ticker, Side: request.OutcomeSide,
		Status: status, Quantity: request.Quantity, LimitPrice: request.LimitPrice, CreatedAt: created,
	}, nil
}

func (c *Client) CancelOrder(ctx context.Context, orderID string) error {
	if !strings.EqualFold(c.cfg.Environment, "demo") {
		return errors.New("kalshi order mutation is locked outside the demo environment")
	}
	if strings.TrimSpace(orderID) == "" {
		return errors.New("kalshi order id is required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/portfolio/events/orders/"+url.PathEscape(orderID), nil, nil, http.StatusOK)
}

type rawOrder struct {
	ID        string    `json:"order_id"`
	Ticker    string    `json:"ticker"`
	Status    string    `json:"status"`
	Side      string    `json:"side"`
	Action    string    `json:"action"`
	YesPrice  string    `json:"yes_price_dollars"`
	NoPrice   string    `json:"no_price_dollars"`
	Filled    string    `json:"fill_count_fp"`
	Remaining string    `json:"remaining_count_fp"`
	Initial   string    `json:"initial_count_fp"`
	Created   time.Time `json:"created_time"`
	CreatedMS int64     `json:"created_ts_ms"`
}

type rawFill struct {
	FillID        string    `json:"fill_id"`
	TradeID       string    `json:"trade_id"`
	OrderID       string    `json:"order_id"`
	Ticker        string    `json:"ticker"`
	MarketTicker  string    `json:"market_ticker"`
	Side          string    `json:"side"`
	OutcomeSide   string    `json:"outcome_side"`
	PurchasedSide string    `json:"purchased_side"`
	Action        string    `json:"action"`
	YesPrice      string    `json:"yes_price_dollars"`
	NoPrice       string    `json:"no_price_dollars"`
	Count         string    `json:"count_fp"`
	FeeCost       string    `json:"fee_cost"`
	IsTaker       bool      `json:"is_taker"`
	Created       time.Time `json:"created_time"`
	TimestampMS   int64     `json:"ts_ms"`
}

type rawPosition struct {
	Ticker         string    `json:"ticker"`
	TotalTraded    string    `json:"total_traded_dollars"`
	Quantity       string    `json:"position_fp"`
	MarketExposure string    `json:"market_exposure_dollars"`
	RealizedPnL    string    `json:"realized_pnl_dollars"`
	FeesPaid       string    `json:"fees_paid_dollars"`
	LastUpdated    time.Time `json:"last_updated_ts"`
}

type rawSettlement struct {
	Ticker       string    `json:"ticker"`
	EventTicker  string    `json:"event_ticker"`
	Result       string    `json:"market_result"`
	YesCount     string    `json:"yes_count_fp"`
	YesTotalCost string    `json:"yes_total_cost_dollars"`
	NoCount      string    `json:"no_count_fp"`
	NoTotalCost  string    `json:"no_total_cost_dollars"`
	RevenueCents int64     `json:"revenue"`
	SettledAt    time.Time `json:"settled_time"`
	FeeCost      string    `json:"fee_cost"`
	ValueCents   int64     `json:"value"`
}

func normalizePosition(raw rawPosition) domain.Position {
	quantity, _ := fixed.Parse(raw.Quantity)
	totalTraded, _ := fixed.Parse(raw.TotalTraded)
	exposure, _ := fixed.Parse(raw.MarketExposure)
	realized, _ := fixed.Parse(raw.RealizedPnL)
	fees, _ := fixed.Parse(raw.FeesPaid)
	side := "yes"
	if quantity < 0 {
		side = "no"
	}
	return domain.Position{
		Exchange: "Kalshi", Ticker: raw.Ticker, Market: raw.Ticker, Side: side,
		Quantity: quantity, CashRisk: absMoney(exposure), TotalTraded: totalTraded,
		RealizedPnL: realized, FeesPaid: fees, UpdatedAt: raw.LastUpdated,
	}
}

func normalizeSettlement(raw rawSettlement) domain.Settlement {
	yesQuantity, _ := fixed.Parse(raw.YesCount)
	noQuantity, _ := fixed.Parse(raw.NoCount)
	yesCost, _ := fixed.Parse(raw.YesTotalCost)
	noCost, _ := fixed.Parse(raw.NoTotalCost)
	fee, _ := fixed.Parse(raw.FeeCost)
	revenue := domain.Money(raw.RevenueCents * 100)
	return domain.Settlement{
		Exchange: "Kalshi", Ticker: raw.Ticker, EventTicker: raw.EventTicker, Result: raw.Result,
		YesQuantity: yesQuantity, NoQuantity: noQuantity, YesTotalCost: yesCost, NoTotalCost: noCost,
		Revenue: revenue, Fee: fee, NetPnL: revenue - yesCost - noCost - fee,
		SettlementValue: domain.Money(raw.ValueCents * 100), SettledAt: raw.SettledAt,
	}
}

func absMoney(value domain.Money) domain.Money {
	if value < 0 {
		return -value
	}
	return value
}

func normalizeFill(raw rawFill) domain.Fill {
	id := raw.FillID
	if id == "" {
		id = raw.TradeID
	}
	ticker := raw.MarketTicker
	if ticker == "" {
		ticker = raw.Ticker
	}
	side := raw.OutcomeSide
	if side == "" {
		side = raw.PurchasedSide
	}
	if side == "" {
		side = raw.Side
	}
	priceText := raw.YesPrice
	if side == "no" && raw.NoPrice != "" {
		priceText = raw.NoPrice
	}
	price, _ := fixed.Parse(priceText)
	if side == "no" && raw.NoPrice == "" {
		price = domain.Dollar - price
	}
	quantity, _ := fixed.Parse(raw.Count)
	fee, _ := fixed.Parse(raw.FeeCost)
	if raw.FeeCost == "" {
		fee = pricing.KalshiFee(price, quantity, !raw.IsTaker)
	}
	quote, _ := pricing.QuoteWithFee(price, quantity, fee)
	created := raw.Created
	if created.IsZero() && raw.TimestampMS > 0 {
		created = time.UnixMilli(raw.TimestampMS).UTC()
	}
	if created.IsZero() {
		created = time.Now().UTC()
	}
	return domain.Fill{ID: id, OrderID: raw.OrderID, Exchange: "Kalshi", Ticker: ticker, Market: ticker, Side: side, Quantity: quantity, RawPrice: price, AllInMoneyline: quote.AllInMoneyline, Fee: fee, CashRisk: quote.AllInCost, CreatedAt: created}
}

func normalizeOrder(raw rawOrder) domain.Order {
	priceText := raw.YesPrice
	if raw.Side == "no" && raw.NoPrice != "" {
		priceText = raw.NoPrice
	}
	price, _ := fixed.Parse(priceText)
	filled, _ := fixed.Parse(raw.Filled)
	remaining, _ := fixed.Parse(raw.Remaining)
	initial, _ := fixed.Parse(raw.Initial)
	if initial == 0 {
		initial = filled + remaining
	}
	created := raw.Created
	if created.IsZero() && raw.CreatedMS > 0 {
		created = time.UnixMilli(raw.CreatedMS).UTC()
	}
	cashRisk := domain.Money(0)
	if remaining > 0 {
		if quote, err := pricing.Quote(price, remaining, false); err == nil {
			cashRisk = quote.AllInCost
		}
	}
	return domain.Order{ID: raw.ID, Exchange: "Kalshi", Ticker: raw.Ticker, Market: raw.Ticker, Side: raw.Side, Status: raw.Status, Quantity: initial, FilledQuantity: filled, LimitPrice: price, CashRisk: cashRisk, CreatedAt: created}
}

func (c *Client) SubscribeAccount(ctx context.Context) (*exchange.Subscription, error) {
	return c.subscribe(ctx, []string{"fill", "user_orders", "market_positions", "market_lifecycle_v2"}, nil, false)
}

func (c *Client) SubscribeBooks(ctx context.Context, tickers []string) (*exchange.Subscription, error) {
	if len(tickers) == 0 {
		return nil, errors.New("at least one market ticker is required for an order-book subscription")
	}
	return c.subscribe(ctx, []string{"ticker", "orderbook_delta"}, tickers, true)
}

func (c *Client) subscribe(ctx context.Context, channels, tickers []string, useYesPrice bool) (*exchange.Subscription, error) {
	if c.key == nil || c.cfg.KeyID == "" {
		return nil, errors.New("kalshi websocket requires PMBATTLE_KALSHI_KEY_ID and private key")
	}
	headers, err := c.authHeaders(http.MethodGet, "/trade-api/ws/v2")
	if err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.wsURL, headers)
	if err != nil {
		return nil, err
	}
	events := make(chan domain.StreamEvent, 256)
	errs := make(chan error, 1)
	params := map[string]any{"channels": channels}
	if useYesPrice {
		params["use_yes_price"] = true
	}
	if len(tickers) > 0 {
		params["market_tickers"] = tickers
	}
	if err := conn.WriteJSON(map[string]any{"id": 1, "cmd": "subscribe", "params": params}); err != nil {
		conn.Close()
		return nil, err
	}
	go func() {
		defer close(events)
		defer close(errs)
		defer conn.Close()
		lastSequence := map[int64]int64{}
		for {
			var msg wsMessage
			if err := conn.ReadJSON(&msg); err != nil {
				select {
				case errs <- err:
				default:
					{
					}
				}
				return
			}
			if msg.SID != 0 && msg.Seq != 0 {
				if previous := lastSequence[msg.SID]; previous != 0 && msg.Seq != previous+1 {
					select {
					case errs <- fmt.Errorf("kalshi websocket sequence gap on subscription %d: got %d after %d", msg.SID, msg.Seq, previous):
					default:
					}
					return
				}
				lastSequence[msg.SID] = msg.Seq
			}
			translated, ok, err := translate(msg)
			if err != nil {
				select {
				case errs <- err:
				default:
					{
					}
				}
				continue
			}
			if ok {
				select {
				case events <- translated:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return &exchange.Subscription{Events: events, Errors: errs, Close: conn.Close}, nil
}

type wsMessage struct {
	Type string          `json:"type"`
	SID  int64           `json:"sid"`
	Seq  int64           `json:"seq"`
	Msg  json.RawMessage `json:"msg"`
}

func translate(message wsMessage) (domain.StreamEvent, bool, error) {
	switch message.Type {
	case "ticker":
		var value map[string]any
		if err := json.Unmarshal(message.Msg, &value); err != nil {
			return domain.StreamEvent{}, false, err
		}
		return domain.StreamEvent{Type: "ticker", Data: value}, true, nil
	case "orderbook_snapshot":
		var raw struct {
			Ticker string     `json:"market_ticker"`
			Yes    [][]string `json:"yes_dollars_fp"`
			No     [][]string `json:"no_dollars_fp"`
		}
		if err := json.Unmarshal(message.Msg, &raw); err != nil {
			return domain.StreamEvent{}, false, err
		}
		book := domain.OrderBook{Ticker: raw.Ticker, Sequence: message.Seq, UpdatedAt: time.Now().UTC()}
		book.Yes = parseLevels(raw.Yes)
		book.No = parseLevels(raw.No)
		return domain.StreamEvent{Type: "orderbook", Data: book}, true, nil
	case "orderbook_delta":
		var raw struct {
			Ticker string `json:"market_ticker"`
			Side   string `json:"side"`
			Price  string `json:"price_dollars"`
			Delta  string `json:"delta_fp"`
		}
		if err := json.Unmarshal(message.Msg, &raw); err != nil {
			return domain.StreamEvent{}, false, err
		}
		price, err := fixed.Parse(raw.Price)
		if err != nil {
			return domain.StreamEvent{}, false, err
		}
		delta, err := fixed.Parse(raw.Delta)
		if err != nil {
			return domain.StreamEvent{}, false, err
		}
		return domain.StreamEvent{Type: "orderbook_delta", Data: domain.OrderBookDelta{Ticker: raw.Ticker, Sequence: message.Seq, Side: raw.Side, Price: price, Delta: delta}}, true, nil
	case "fill":
		var raw rawFill
		if err := json.Unmarshal(message.Msg, &raw); err != nil {
			return domain.StreamEvent{}, false, err
		}
		return domain.StreamEvent{Type: "fill", Data: normalizeFill(raw)}, true, nil
	case "user_order":
		var raw rawOrder
		if err := json.Unmarshal(message.Msg, &raw); err != nil {
			return domain.StreamEvent{}, false, err
		}
		order := normalizeOrder(raw)
		return domain.StreamEvent{Type: "order", Data: order}, true, nil
	case "market_position":
		var raw struct {
			Ticker   string `json:"market_ticker"`
			Quantity string `json:"position_fp"`
			Cost     string `json:"position_cost_dollars"`
		}
		if err := json.Unmarshal(message.Msg, &raw); err != nil {
			return domain.StreamEvent{}, false, err
		}
		quantity, _ := fixed.Parse(raw.Quantity)
		cost, _ := fixed.Parse(raw.Cost)
		average := domain.Money(0)
		if quantity != 0 {
			average = absMoney(domain.Money(int64(cost) * int64(domain.Dollar) / int64(quantity)))
		}
		side := "yes"
		if quantity < 0 {
			side = "no"
		}
		position := domain.Position{Exchange: "Kalshi", Ticker: raw.Ticker, Market: raw.Ticker, Side: side, Quantity: quantity, CashRisk: absMoney(cost), AveragePrice: average, UpdatedAt: time.Now().UTC()}
		return domain.StreamEvent{Type: "position", Data: position}, true, nil
	case "market_lifecycle_v2":
		return domain.StreamEvent{Type: "market_lifecycle", Data: json.RawMessage(message.Msg)}, true, nil
	default:
		return domain.StreamEvent{}, false, nil
	}
}

func parseLevels(raw [][]string) []domain.BookLevel {
	levels := make([]domain.BookLevel, 0, len(raw))
	for _, row := range raw {
		if len(row) < 2 {
			continue
		}
		p, e1 := fixed.Parse(row[0])
		q, e2 := fixed.Parse(row[1])
		if e1 == nil && e2 == nil {
			levels = append(levels, domain.BookLevel{Price: p, Quantity: q})
		}
	}
	return levels
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	headers, err := c.authHeaders(http.MethodGet, "/trade-api/v2"+strings.Split(path, "?")[0])
	if err != nil {
		return err
	}
	req.Header = headers
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kalshi %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) doJSON(ctx context.Context, method, path string, input, output any, expected ...int) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	headers, err := c.authHeaders(method, "/trade-api/v2"+path)
	if err != nil {
		return err
	}
	req.Header = headers
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	accepted := false
	for _, status := range expected {
		accepted = accepted || resp.StatusCode == status
	}
	if !accepted {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("kalshi %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(message)))
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(output)
}

func (c *Client) authHeaders(method, path string) (http.Header, error) {
	if c.key == nil {
		return nil, errors.New("kalshi private key not configured")
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	digest := sha256.Sum256([]byte(timestamp + method + path))
	signature, err := rsa.SignPSS(rand.Reader, c.key, crypto.SHA256, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("KALSHI-ACCESS-KEY", c.cfg.KeyID)
	headers.Set("KALSHI-ACCESS-TIMESTAMP", timestamp)
	headers.Set("KALSHI-ACCESS-SIGNATURE", base64.StdEncoding.EncodeToString(signature))
	return headers, nil
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid PEM private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
