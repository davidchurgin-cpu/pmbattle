# PMBattle Engineering Handoff

## Current milestone

Milestone 1—the read-only terminal foundation—is implemented. Milestone 2 now has guarded basic, iceberg, and controlled follow slices. Limit, post-only, IOC basic orders, limit/post-only icebergs, and post-only follow parents flow through one durable cash-at-risk model into Kalshi's V2 demo order API. Parent state survives restart and reconciles order-scoped fills, paginated open positions, and settled-market history before the browser receives account activity. Simulated mode and production connections remain read-only.

## Architecture

- `main.go` loads configuration, opens SQLite, starts the application service, embeds the frontend, and shuts down gracefully.
- `internal/schedule` downloads and normalizes the sportsbook XML feed.
- `internal/kalshi` implements environment selection, RSA-PSS authentication, market discovery, and separate account/order-book WebSocket subscriptions.
- `internal/mapping` conservatively matches exchange markets to canonical schedule events.
- `internal/pricing` calculates current Kalshi maker/taker fees and fee-adjusted American moneylines using fixed-point money.
- `internal/live` maintains sequence-checked in-memory order books.
- `internal/orders` validates and sizes fee-inclusive parent orders, enforces the moneyline cap, links child orders, and owns demo iceberg, follow, and cancellation state.
- `internal/storage` owns SQLite WAL tables for events, mappings, durable parent orders, settlements, settings, and audit history.
- `internal/app` coordinates polling, mapping, reconciliation, streaming, and the browser snapshot.
- `internal/server` exposes JSON and WebSocket endpoints, with mutation handlers delegated to the demo-only service guard, and serves the embedded app.
- `web/src/App.svelte` contains the lightweight sportsbook board, instant search, filters, click-to-expand inline book ladder, and bottom activity tray.
- `web/src/orderslip.css` isolates the small floating order-slip surface from the critical board styles.
- `web/src/monitor.css` contains the fixed order monitor and transient fill-alert styling.
- `web/src/sides.css` owns the shared Away/Home/Over/Under color system and labels.

## Browser API

- `GET /api/health` — service and feed state
- `GET /api/snapshot` — events, open positions, recent settlements, managed orders, bankroll, and health
- `GET /api/books/{ticker}` — select the UI order book; returns `202` while its first live snapshot is opening
- `DELETE /api/books/{ticker}` — release the UI book when the game dropdown closes; strategy-required books remain subscribed
- `GET /api/settings` — available sports, event counts, and saved preferences
- `PUT /api/settings` — save enabled sports and refresh the schedule and exchange subscriptions
- `POST /api/parent-orders` — create a cash-risk-bounded basic, iceberg, or follow order; returns `403` unless explicitly enabled in Kalshi demo mode
- `DELETE /api/parent-orders/{id}` — cancel every child of a demo parent order; also locked outside demo mode
- `POST /api/parent-orders/cancel` — cancel active managed parents by `all`, `event`, `strategy`, or `exchange`; returns `207` with per-parent failures after partial success
- `POST /api/parent-orders/{id}/resume` — manually resume an error-paused follow parent after fresh-book revalidation; locked outside demo mode
- `GET /api/ws` — compact live events: `schedule`, `account_snapshot` (including settlements), `health`, `ticker`, `orderbook`, `book_stale`, `fill`, `parent_order`, `order`, `position`, and `market_lifecycle`

The mutation routes are present for demo validation but are inert by default. Startup refuses `PMBATTLE_TRADING_ENABLED=true` in simulated or production mode, and the Kalshi client separately refuses place, amend, and cancel calls unless configured for `demo`. No real-money mutation may be sent without the user's explicit permission at that time.

## Important operational details

- The office server's existing IP allowlist remains the access boundary. PMBattle should sit behind its normal TLS/reverse-proxy setup.
- Use a dedicated server directory for `pmbattle.db` and back it up normally.
- Use the server's secret/environment manager for the Kalshi key ID and PEM path.
- Start with Kalshi demo. Confirm the health indicator and market mappings before considering production data.
- The Kalshi adapter requests only enabled schedule leagues and main `GAME`, `SPREAD`, and `TOTAL` event series. Multileg and prop catalogs are outside the main board path.
- The market matcher uses Kalshi's authoritative two-team event title plus occurrence time. Both participants must match; ambiguous duplicate matchups remain `review` and are hidden.
- Main spread and total lines are selected from the active strike closest to a 50% midpoint. Up to five nearby strikes are retained for the inline line selector.
- Clicking a game expands its order book in place. The consolidated book stream contains the selected UI ticker plus the unique tickers required by active follow parents; changing or closing the dropdown never stops a working follow strategy. All unrelated books remain unloaded. The authenticated account stream remains independent and continuously connected.
- The Yes and No tabs are real views of the same binary book: the No ladder is derived by complementing the synchronized Yes-price book. Clicking any bid or ask copies its exact side and price into the floating order slip. Submission stays disabled on the running production connection.
- The dashboard monitor remains fixed while the user searches or changes markets. It shows normalized remaining quantities for working orders and the three latest fills. Each new WebSocket fill produces a 12-second visual alert and unread count; snapshot/replayed fill IDs are suppressed.
- Every quote carries its explicit Kalshi `yes`/`no` contract side. Selecting Away, Home, Over, or Under initializes the correct book side, while labels and four accessible colors remain consistent through the board, expanded book, and order slip.
- REST and WebSocket orders are decoded from Kalshi's current `*_dollars` and `*_count_fp` fields into fixed-point internal values. This is required for reliable remaining-quantity and cash-risk monitoring.
- Demo orders use Kalshi's current `/portfolio/events/orders` V2 shape. Buying NO is emitted as an ask at the complementary YES-book price. Counts and prices remain four-decimal fixed-point strings.
- Parent sizing uses the conservative taker fee even for post-only orders. A binary search selects the greatest fractional contract quantity whose all-in cost does not exceed the cash-risk target; large low-price calculations use overflow-safe integer arithmetic.
- An iceberg persists every child ID, client ID, quantity, fill quantity, and lifecycle state. Only one nonterminal slice is exposed at once. Partial fills retain that slice; a full slice creates exactly one replacement capped by both the configured slice and remaining fee-inclusive risk.
- Refresh errors pause the parent, duplicate fill IDs never refresh again, cancellation skips terminal slices, and per-fill fee rounding can shrink the remaining parent quantity. If a partially filled working child would exceed remaining risk, the demo engine cancels it before publishing the fill. A late fill received after cancellation still updates filled risk but cannot restart or re-cancel the strategy.
- Follow creation ignores the browser's displayed price and reads the current synchronized server book. YES follows the highest YES bid; NO follows the complement of the lowest YES ask. Every child is post-only, so it cannot automatically cross the spread.
- A follow amendment occurs only when the same-side top price changes, remains inside the hard fee-adjusted moneyline cap, and the 750 ms cooldown has elapsed. Repricing resizes the total/max-fillable count within remaining cash risk, rotates the client order ID, persists the decision, and publishes parent/order state before the new book reaches the browser.
- A stale book changes the parent to `paused_stale` without mutating the exchange. A price beyond the cap changes it to `price_capped` while leaving the safer resting child in place. Fresh acceptable data resumes the parent; an exchange amend error changes it to `paused` for manual review.
- A generic `paused` follow never retries automatically. Its demo-only Resume control first confirms a synchronized book and an active nonterminal child, clears the amendment cooldown, and feeds the current book through the normal fee-cap and remaining-risk path. Canceled, filled, non-follow, missing-child, stale-book, and production resumes are rejected.
- The Orders tray exposes a compact demo kill switch for all managed parents, the currently expanded game, each strategy, or Kalshi. The control is absent while trading is locked. Scoped cancellation filters only nonterminal PMBattle parents, calls the normal parent cancellation path, and reports partial failures instead of rolling back acknowledged cancels.
- Every fill carries its exchange order ID. The engine applies each fill ID once, updates filled quantity/risk, reduces the remaining reservation, persists the parent, and only then publishes the parent followed by the fill. Filled risk remains included in the station-wide cash-at-risk total.
- Startup and account-stream reconnects query Kalshi fill history by PMBattle child order ID and replay it oldest-first. The initial `account_snapshot` refresh is quiet so recovered fills do not look like new live alerts.
- The account snapshot also reads Kalshi's available balance in cents and converts it into PMBattle's four-decimal fixed-point bankroll without floating-point math.
- Account restart/reconnect reconciliation follows every `cursor` page for resting orders and nonzero, unsettled positions. Signed position quantities become explicit YES/NO sides, while exposure, traded amount, realized P&L, and fees remain fixed-point.
- Settled markets come from Kalshi's separate paginated settlement endpoint. PMBattle uses a one-second overlap on incremental reads, upserts by exchange/ticker in SQLite, retains the 500 latest records in the browser snapshot, and shows revenue, fees, and net P&L in History. Settlements are deliberately excluded from current cash at risk.
- Kalshi sequence numbers are subscription-wide, not ticker-wide. A book-stream gap forces the consolidated UI/strategy subscription to reconnect and marks every cached member stale until fresh snapshots arrive.
- Sports preferences are stored in SQLite. No saved preference means all sports; saving an empty selection intentionally loads no sports.
- Extra/added games are identified by an exactly six-digit numeric schedule event ID. The Settings tab can exclude them before market matching and subscription.
- Simulated events include selectable moneyline, spread, and total quotes. Six-digit added games use lower simulated available quantities.

## Known limitations

- Kalshi live order authentication and on-demand book streaming have been validated read-only against a production account. Validation mapped 2,240 contracts into 441 selectable game/strike books; a requested book moved from `202` to a synchronized live ladder, switching tickers opened only the replacement stream, and release returned `204`.
- Initial league-to-series routing covers the major US leagues plus selected top soccer leagues. Add aliases as new schedule leagues are enabled; unknown leagues intentionally load no Kalshi series.
- The current general Kalshi fee rule is versioned in one module, but market-specific fee exceptions must be added before any production order preview.
- Follow has automated coverage with a fake demo adapter and the current V2 amend contract, but it has not yet been manually exercised with separate Kalshi demo credentials. Production remains hard locked.
- Completed parents are retained in SQLite without pruning yet. A retention policy will be needed as history grows.
- Open-position and settlement restart reconciliation has recorded multi-page fixture coverage, but the new History display still needs a read-only smoke test against the production account after deployment.
- Production mutation is intentionally impossible and must remain so unless a separate review is completed and the user explicitly authorizes real-money trading.
- The schedule feed is HTTP. Deploy through the office server and monitor its freshness; do not infer a game state when the feed is unavailable.

## Next milestone: Kalshi demo trading

1. Read-only smoke test the open-position and settlement History views against production data without sending any mutation.
2. Validate basic, iceberg, follow, resume, and scoped-cancel flows manually with separate Kalshi demo credentials, without sending any production mutation.
3. Keep production blocked until demo fees, partial fills, reconnect recovery, and risk totals match Kalshi reports and the user explicitly authorizes a later real-money milestone.

## Validation commands

```text
cd web && npm ci && npm run check && npm run build
go test ./...
go build .
```

GitHub Actions runs these checks and publishes portable Windows/Linux binaries on each push to `main`.

## Lightweight checkpoint

- Browser production bundle: about 80.4 KB JavaScript and 20.8 KB CSS before gzip; there are no runtime browser dependencies beyond Svelte.
- Production source maps are disabled and old side-panel CSS/dead book code were removed.
- Runtime background work is bounded: one 30-second schedule ticker, one authenticated account stream, and one consolidated order-book stream containing only the selected UI book plus active follow books.
- The stripped single Windows executable is about 12.0 MB, primarily because it embeds the pure-Go SQLite implementation and the complete browser app; deployment still requires only that one executable.
