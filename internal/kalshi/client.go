package kalshi

import (
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
	"net/http"
	"net/url"
	"os"
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

type marketResponse struct {
	Markets []market `json:"markets"`
	Cursor  string   `json:"cursor"`
}
type market struct {
	Ticker      string    `json:"ticker"`
	EventTicker string    `json:"event_ticker"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	YesSubTitle string    `json:"yes_sub_title"`
	NoSubTitle  string    `json:"no_sub_title"`
	CloseTime   time.Time `json:"close_time"`
	YesBid      string    `json:"yes_bid_dollars"`
	YesAsk      string    `json:"yes_ask_dollars"`
	YesBidSize  string    `json:"yes_bid_size_fp"`
	YesAskSize  string    `json:"yes_ask_size_fp"`
}

func (c *Client) ListMarkets(ctx context.Context) ([]domain.CanonicalMarket, error) {
	var result []domain.CanonicalMarket
	cursor := ""
	for page := 0; page < 20; page++ {
		endpoint := c.baseURL + "/markets?status=open&limit=1000"
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
			return nil, fmt.Errorf("kalshi markets: %s", resp.Status)
		}
		var payload marketResponse
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, m := range payload.Markets {
			bid, _ := fixed.Parse(m.YesBid)
			ask, _ := fixed.Parse(m.YesAsk)
			bidSize, _ := fixed.Parse(m.YesBidSize)
			askSize, _ := fixed.Parse(m.YesAskSize)
			result = append(result, domain.CanonicalMarket{ID: m.Ticker, Exchange: "kalshi", ExchangeTicker: m.Ticker, Title: m.Title, Subtitle: strings.TrimSpace(m.Subtitle + " " + m.YesSubTitle + " " + m.NoSubTitle), CloseTime: m.CloseTime, YesBid: bid, YesAsk: ask, YesBidSize: bidSize, YesAskSize: askSize, MappingStatus: "review"})
		}
		if payload.Cursor == "" {
			break
		}
		cursor = payload.Cursor
	}
	return result, nil
}

func (c *Client) Snapshot(ctx context.Context) ([]domain.Order, []domain.Position, []domain.Fill, error) {
	if c.key == nil || c.cfg.KeyID == "" {
		return nil, nil, nil, nil
	}
	var ordersPayload struct {
		Orders []domain.Order `json:"orders"`
	}
	if err := c.getJSON(ctx, "/portfolio/orders?status=resting", &ordersPayload); err != nil {
		return nil, nil, nil, err
	}
	// Raw portfolio shapes vary across API migrations; keep reconciliation tolerant
	// and let the WebSocket streams populate normalized position/fill records.
	return ordersPayload.Orders, nil, nil, nil
}

func (c *Client) Subscribe(ctx context.Context, tickers []string) (*exchange.Subscription, error) {
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
	channels := []string{"ticker", "orderbook_delta", "fill", "user_orders", "market_positions", "market_lifecycle_v2"}
	params := map[string]any{"channels": channels}
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
		var raw struct {
			TradeID       string `json:"trade_id"`
			OrderID       string `json:"order_id"`
			Ticker        string `json:"market_ticker"`
			IsTaker       bool   `json:"is_taker"`
			Side          string `json:"side"`
			PurchasedSide string `json:"purchased_side"`
			Action        string `json:"action"`
			Price         string `json:"yes_price_dollars"`
			Count         string `json:"count_fp"`
			TimestampMS   int64  `json:"ts_ms"`
		}
		if err := json.Unmarshal(message.Msg, &raw); err != nil {
			return domain.StreamEvent{}, false, err
		}
		price, err := fixed.Parse(raw.Price)
		if err != nil {
			return domain.StreamEvent{}, false, err
		}
		quantity, err := fixed.Parse(raw.Count)
		if err != nil {
			return domain.StreamEvent{}, false, err
		}
		if raw.Side == "no" || raw.PurchasedSide == "no" {
			price = domain.Dollar - price
		}
		fee := pricing.KalshiFee(price, quantity, !raw.IsTaker)
		quote, _ := pricing.Quote(price, quantity, !raw.IsTaker)
		created := time.UnixMilli(raw.TimestampMS).UTC()
		if raw.TimestampMS == 0 {
			created = time.Now().UTC()
		}
		fill := domain.Fill{ID: raw.TradeID, Exchange: "Kalshi", Ticker: raw.Ticker, Market: raw.Ticker, Side: raw.Side, Quantity: quantity, RawPrice: price, AllInMoneyline: quote.AllInMoneyline, Fee: fee, CashRisk: quote.AllInCost, CreatedAt: created}
		return domain.StreamEvent{Type: "fill", Data: fill}, true, nil
	case "user_order":
		var raw struct {
			ID      string    `json:"order_id"`
			Ticker  string    `json:"ticker"`
			Status  string    `json:"status"`
			Side    string    `json:"side"`
			Price   string    `json:"yes_price_dollars"`
			Filled  string    `json:"fill_count_fp"`
			Initial string    `json:"initial_count_fp"`
			Created time.Time `json:"created_time"`
		}
		if err := json.Unmarshal(message.Msg, &raw); err != nil {
			return domain.StreamEvent{}, false, err
		}
		price, _ := fixed.Parse(raw.Price)
		quantity, _ := fixed.Parse(raw.Initial)
		filled, _ := fixed.Parse(raw.Filled)
		order := domain.Order{ID: raw.ID, Exchange: "Kalshi", Ticker: raw.Ticker, Market: raw.Ticker, Side: raw.Side, Status: raw.Status, Quantity: quantity, FilledQuantity: filled, LimitPrice: price, CashRisk: domain.Money(int64(price) * int64(quantity) / int64(domain.Dollar)), CreatedAt: raw.Created}
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
			average = domain.Money(int64(cost) * int64(domain.Dollar) / int64(quantity))
		}
		position := domain.Position{Exchange: "Kalshi", Ticker: raw.Ticker, Market: raw.Ticker, Quantity: quantity, CashRisk: cost, AveragePrice: average}
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
