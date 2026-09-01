# PMBattle Engineering Handoff

## Current milestone

Milestone 1—the read-only terminal foundation—is implemented. Milestone 2 now has a guarded basic-order vertical slice: limit, post-only, IOC, and cancel flow through one durable cash-at-risk parent order into Kalshi's V2 demo order API. Parent state survives restart and reconciles order-scoped fill history before the browser receives fill activity. Simulated mode and production connections remain read-only by default.

## Architecture

- `main.go` loads configuration, opens SQLite, starts the application service, embeds the frontend, and shuts down gracefully.
- `internal/schedule` downloads and normalizes the sportsbook XML feed.
- `internal/kalshi` implements environment selection, RSA-PSS authentication, market discovery, and separate account/order-book WebSocket subscriptions.
- `internal/mapping` conservatively matches exchange markets to canonical schedule events.
- `internal/pricing` calculates current Kalshi maker/taker fees and fee-adjusted American moneylines using fixed-point money.
- `internal/live` maintains sequence-checked in-memory order books.
- `internal/orders` validates and sizes fee-inclusive parent orders, enforces the moneyline cap, links child orders, and owns demo cancellation state.
- `internal/storage` owns SQLite WAL tables for events, mappings, durable parent orders, settings, and audit history.
- `internal/app` coordinates polling, mapping, reconciliation, streaming, and the browser snapshot.
- `internal/server` exposes JSON and WebSocket endpoints, with mutation handlers delegated to the demo-only service guard, and serves the embedded app.
- `web/src/App.svelte` contains the lightweight sportsbook board, instant search, filters, click-to-expand inline book ladder, and bottom activity tray.
- `web/src/orderslip.css` isolates the small floating order-slip surface from the critical board styles.
- `web/src/monitor.css` contains the fixed order monitor and transient fill-alert styling.
- `web/src/sides.css` owns the shared Away/Home/Over/Under color system and labels.

## Browser API

- `GET /api/health` — service and feed state
- `GET /api/snapshot` — events, account state, bankroll, and health
- `GET /api/books/{ticker}` — request the one active order book; returns `202` while its first live snapshot is opening
- `DELETE /api/books/{ticker}` — release the active book when the game dropdown closes
- `GET /api/settings` — available sports, event counts, and saved preferences
- `PUT /api/settings` — save enabled sports and refresh the schedule and exchange subscriptions
- `POST /api/parent-orders` — create a cash-risk-bounded basic order; returns `403` unless explicitly enabled in Kalshi demo mode
- `DELETE /api/parent-orders/{id}` — cancel every child of a demo parent order; also locked outside demo mode
- `GET /api/ws` — compact live events: `schedule`, `account_snapshot`, `health`, `ticker`, `orderbook`, `book_stale`, `fill`, `parent_order`, `order`, `position`, and `market_lifecycle`

The mutation routes are present for demo validation but are inert by default. Startup refuses `PMBATTLE_TRADING_ENABLED=true` in simulated or production mode, and the Kalshi client separately refuses every order mutation unless configured for `demo`.

## Important operational details

- The office server's existing IP allowlist remains the access boundary. PMBattle should sit behind its normal TLS/reverse-proxy setup.
- Use a dedicated server directory for `pmbattle.db` and back it up normally.
- Use the server's secret/environment manager for the Kalshi key ID and PEM path.
- Start with Kalshi demo. Confirm the health indicator and market mappings before considering production data.
- The Kalshi adapter requests only enabled schedule leagues and main `GAME`, `SPREAD`, and `TOTAL` event series. Multileg and prop catalogs are outside the main board path.
- The market matcher uses Kalshi's authoritative two-team event title plus occurrence time. Both participants must match; ambiguous duplicate matchups remain `review` and are hidden.
- Main spread and total lines are selected from the active strike closest to a 50% midpoint. Up to five nearby strikes are retained for the inline line selector.
- Clicking a game expands its order book in place. Only the selected ticker receives a book subscription; selecting another ticker cancels the old stream, and closing the dropdown releases it. The authenticated account stream remains independent and continuously connected.
- The Yes and No tabs are real views of the same binary book: the No ladder is derived by complementing the synchronized Yes-price book. Clicking any bid or ask copies its exact side and price into the floating order slip. Submission stays disabled on the running production connection.
- The dashboard monitor remains fixed while the user searches or changes markets. It shows normalized remaining quantities for working orders and the three latest fills. Each new WebSocket fill produces a 12-second visual alert and unread count; snapshot/replayed fill IDs are suppressed.
- Every quote carries its explicit Kalshi `yes`/`no` contract side. Selecting Away, Home, Over, or Under initializes the correct book side, while labels and four accessible colors remain consistent through the board, expanded book, and order slip.
- REST and WebSocket orders are decoded from Kalshi's current `*_dollars` and `*_count_fp` fields into fixed-point internal values. This is required for reliable remaining-quantity and cash-risk monitoring.
- Demo orders use Kalshi's current `/portfolio/events/orders` V2 shape. Buying NO is emitted as an ask at the complementary YES-book price. Counts and prices remain four-decimal fixed-point strings.
- Parent sizing uses the conservative taker fee even for post-only orders. A binary search selects the greatest fractional contract quantity whose all-in cost does not exceed the cash-risk target; large low-price calculations use overflow-safe integer arithmetic.
- Every fill carries its exchange order ID. The engine applies each fill ID once, updates filled quantity/risk, reduces the remaining reservation, persists the parent, and only then publishes the parent followed by the fill. Filled risk remains included in the station-wide cash-at-risk total.
- Startup and account-stream reconnects query Kalshi fill history by PMBattle child order ID and replay it oldest-first. The initial `account_snapshot` refresh is quiet so recovered fills do not look like new live alerts.
- The account snapshot also reads Kalshi's available balance in cents and converts it into PMBattle's four-decimal fixed-point bankroll without floating-point math.
- Kalshi sequence numbers are subscription-wide, not ticker-wide. A book-stream gap forces that selected ticker to reconnect and remain stale until a new snapshot arrives.
- Sports preferences are stored in SQLite. No saved preference means all sports; saving an empty selection intentionally loads no sports.
- Extra/added games are identified by an exactly six-digit numeric schedule event ID. The Settings tab can exclude them before market matching and subscription.
- Simulated events include selectable moneyline, spread, and total quotes. Six-digit added games use lower simulated available quantities.

## Known limitations

- Kalshi live order authentication and on-demand book streaming have been validated read-only against a production account. Validation mapped 2,240 contracts into 441 selectable game/strike books; a requested book moved from `202` to a synchronized live ladder, switching tickers opened only the replacement stream, and release returned `204`. Position/fill historical REST reconciliation remains the next account-data task.
- Initial league-to-series routing covers the major US leagues plus selected top soccer leagues. Add aliases as new schedule leagues are enabled; unknown leagues intentionally load no Kalshi series.
- The current general Kalshi fee rule is versioned in one module, but market-specific fee exceptions must be added before any production order preview.
- Iceberg and follow controls remain visible previews but are rejected by the engine until their lifecycle logic is implemented.
- Completed parents are retained in SQLite without pruning yet. A retention policy will be needed as history grows.
- REST position history and settlements are not yet part of restart reconciliation; live position WebSocket updates are implemented.
- Production mutation is intentionally impossible and must remain so unless a separate review is completed and the user explicitly authorizes real-money trading.
- The schedule feed is HTTP. Deploy through the office server and monitor its freshness; do not infer a game state when the feed is unavailable.

## Next milestone: Kalshi demo trading

1. Add REST position and settlement reconciliation with recorded fixtures.
2. Add cancel/replace while preserving the parent cash-risk reservation and price cap.
3. Add iceberg slicing and throttled join-the-top behavior without automatic spread crossing.
4. Add event, strategy, exchange, and global demo cancel controls.
5. Validate the complete flow manually with separate Kalshi demo credentials, without sending any production mutation.
6. Keep production blocked until demo fees, partial fills, reconnect recovery, and risk totals match Kalshi reports and the user explicitly authorizes a later real-money milestone.

## Validation commands

```text
cd web && npm ci && npm run check && npm run build
go test ./...
go build .
```

GitHub Actions runs these checks and publishes portable Windows/Linux binaries on each push to `main`.

## Lightweight checkpoint

- Browser production bundle: about 74.9 KB JavaScript and 19.4 KB CSS before gzip; there are no runtime browser dependencies beyond Svelte.
- Production source maps are disabled and old side-panel CSS/dead book code were removed.
- Runtime background work is bounded: one 30-second schedule ticker, one authenticated account stream, and zero or one selected order-book stream.
- The stripped single Windows executable is about 12.0 MB, primarily because it embeds the pure-Go SQLite implementation and the complete browser app; deployment still requires only that one executable.
