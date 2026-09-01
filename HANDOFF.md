# PMBattle Engineering Handoff

## Current milestone

Milestone 1—the read-only terminal foundation—is implemented. Milestone 2 has basic, iceberg, and controlled follow orders. Limit, post-only, IOC basic orders, limit/post-only icebergs, and post-only follow parents flow through one durable cash-at-risk model into Kalshi's V2 order API. Parent state survives restart and reconciles order-scoped fills, paginated open positions, and settled-market history before the browser receives account activity. Order entry is disabled by default but can now be enabled for demo or production.

## Architecture

- `main.go` loads configuration, opens SQLite, starts the application service, embeds the frontend, and shuts down gracefully.
- `internal/schedule` downloads and normalizes the sportsbook XML feed.
- `internal/kalshi` implements environment selection, RSA-PSS authentication, market discovery, and separate account/order-book WebSocket subscriptions.
- `internal/mapping` conservatively matches exchange markets to canonical schedule events and supplies evidence-bounded candidates for manual review.
- `internal/pricing` calculates current Kalshi maker/taker fees and fee-adjusted American moneylines using fixed-point money.
- `internal/live` maintains sequence-checked in-memory order books.
- `internal/orders` validates and sizes fee-inclusive parent orders, enforces the moneyline cap, links child orders, and owns demo iceberg, follow, and cancellation state.
- `internal/routing` deterministically plans a single parent cash-risk target across exchange price levels using fee-included price, shared venue cash, hidden commitments, liquidity, freshness, and the hard all-in price cap. It does not execute orders.
- `internal/storage` owns SQLite WAL tables for events, automatic mappings, manual overrides, grouped review payloads, durable parent orders, settlements, settings, and audit history.
- `internal/app` coordinates polling, mapping, reconciliation, streaming, and the browser snapshot.
- `internal/server` exposes JSON and WebSocket endpoints, with mutation handlers delegated to the startup-controlled service guard, and serves the embedded app.
- `web/src/App.svelte` contains the lightweight sportsbook board, instant search, filters, click-to-expand inline book ladder, and bottom activity tray.
- `web/src/orderslip.css` isolates the small floating order-slip surface from the critical board styles.
- `web/src/monitor.css` contains order-action and transient fill-alert styling; the unified fixed activity dock lives in the main board stylesheet.
- `web/src/sides.css` owns the shared Away/Home/Over/Under color system and labels.

## Browser API

- `GET /api/session` — `{loginRequired, authenticated}`; always reachable so the page knows whether to show the sign-in screen
- `POST /api/login` — `{password}`; sets the `pmbattle_session` cookie (HTTP-only, same-site strict, 12 hours); five failures from one address lock sign-in for 15 minutes
- `POST /api/logout` — ends the session
- `GET /api/health` — service and feed state
- `GET /api/snapshot` — events, open positions, recent settlements, managed orders, bankroll, and health
- `GET /api/books/{ticker}` — select the UI order book; returns `202` while its first live snapshot is opening
- `DELETE /api/books/{ticker}` — release the UI book when the game dropdown closes; strategy-required books remain subscribed
- `GET /api/settings` — available sports, event counts, and saved preferences
- `GET /api/audit?limit=100&before={id}` — newest-first immutable audit records, loaded on demand and capped at 200 per page
- `GET /api/mapping-reviews?limit=250` — grouped ambiguous-market reviews, loaded only from Settings and capped at 500
- `POST /api/mapping-reviews/{id}` — accept one listed schedule candidate with `{eventId}` or persistently reject the group with `{reject:true}`; this changes local mapping state only
- `PUT /api/settings` — save enabled sports and refresh the schedule and exchange subscriptions
- `POST /api/parent-orders` — create a cash-risk-bounded basic, iceberg, or follow order; returns `403` unless trading was explicitly enabled at startup
- `DELETE /api/parent-orders/{id}` — cancel every child of a managed parent order; locked when server trading is disabled
- `POST /api/parent-orders/cancel` — cancel active managed parents by `all`, `event`, `strategy`, or `exchange`; returns `207` with per-parent failures after partial success
- `POST /api/parent-orders/{id}/resume` — manually resume an error-paused follow parent after fresh-book revalidation; locked when server trading is disabled
- `GET /api/ws` — compact live events: `schedule`, `account_snapshot` (including settlements), `health`, `ticker`, `orderbook`, `book_stale`, `fill`, `parent_order`, `order`, `position`, and `market_lifecycle`

Every non-GET `/api` request must carry `X-Requested-With: PMBattle`, or it is rejected with 403 before any handler runs; the browser helper `api()` in `App.svelte` adds it to every call. When `PMBATTLE_PASSWORD` is set, every `/api` route except `session` and `login` (the stream included) returns 401 without a valid session cookie, and the page shows a sign-in screen. Static files are always served so the page can render that screen.

Mutation routes are inert by default. Set `PMBATTLE_SIMULATED=false`, choose `PMBATTLE_KALSHI_ENV=demo` or `production`, set `PMBATTLE_PASSWORD` (8+ characters; the server refuses to start with trading on and no password), and set `PMBATTLE_TRADING_ENABLED=true` to enable them. The Kalshi client receives the same startup flag and independently rejects place, amend, and cancel calls when it is off. Production submissions receive an explicit browser confirmation and are labeled `REAL ORDERS` / `LIVE TRADING`.

## Running the project

From a fresh clone, install and embed the browser app, then build the Go server:

```text
cd web
npm ci
npm run build
cd ..
go build -o pmbattle.exe .
```

For Windows PowerShell production testing, configure the machine-local credentials and start the executable:

```powershell
$env:PMBATTLE_KALSHI_ENV = "production"
$env:PMBATTLE_KALSHI_KEY_ID = "your-key-id"
$env:PMBATTLE_KALSHI_PRIVATE_KEY_PATH = "C:\secure\kalshi-private-key.pem"
$env:PMBATTLE_SIMULATED = "false"
$env:PMBATTLE_TRADING_ENABLED = "true"
.\pmbattle.exe
```

Open `http://127.0.0.1:8080/`. Use `PMBATTLE_TRADING_ENABLED=false` for read-only operation. Credentials and private keys stay outside Git and must be configured separately on every computer. Linux uses the same environment names and `go build -o pmbattle .`; portable Windows/Linux artifacts are also published by GitHub Actions.

## Important operational details

- The office server's existing IP allowlist remains the outer boundary, and `PMBATTLE_PASSWORD` is the inner one. PMBattle should sit behind the normal TLS/reverse-proxy setup; the session cookie is marked Secure automatically when the request arrived over TLS or with `X-Forwarded-Proto: https`.
- `internal/server/auth.go` owns sign-in: SHA-256 digest compared in constant time, random 32-byte session tokens kept in memory (a restart signs everyone out), a per-address failure window, and the request-header check for every mutating call.
- The Settings safety panel is informational only and cannot arm trading. It surfaces environment, feed/account sync, last account refresh, mapped markets, available cash, cash at risk, and the immutable startup lock reason. When trading is enabled, its note and the Orders-tray kill switch label now say whether real production orders or demo orders are armed; an earlier version wrongly said production was still blocked.
- History separates exchange settlements from System audit. Audit records are never included in the startup snapshot or WebSocket stream; opening that subview requests the newest 100 records, and Load earlier follows the opaque numeric cursor. Each row has a compact lifecycle summary and a collapsible exact JSON payload.
- Use a dedicated server directory for `pmbattle.db` and back it up normally.
- Use the server's secret/environment manager for the Kalshi key ID and PEM path.
- Start with Kalshi demo. Confirm the health indicator and market mappings before considering production data.
- The Kalshi adapter requests only enabled schedule leagues and main `GAME`, `SPREAD`, and `TOTAL` event series. Multileg and prop catalogs are outside the main board path.
- The market matcher uses Kalshi's authoritative two-team event title plus occurrence time. Both participants must match; ambiguous duplicate matchups remain `review` and are hidden.
- The manual review queue groups contracts sharing exchange, Kalshi event title, and occurrence time so a single decision covers related tickers. Candidates require positive two-team evidence and a start time within 36 hours. A decision can select only one of those server-issued candidates; contracts with no safe candidate stay hidden and do not flood the queue.
- Accepted and rejected decisions are stored in a separate override table, survive catalog refreshes/restarts, are applied before tradable books are attached, and create an immutable `mapping_review_decided` audit record. Accepting or rejecting never calls an exchange mutation endpoint.
- Main spread and total lines are selected from the active strike closest to a 50% midpoint. Up to five nearby strikes are retained for the inline line selector.
- Clicking a game expands its order book in place. The consolidated book stream contains the selected UI ticker plus the unique tickers required by active follow parents; changing or closing the dropdown never stops a working follow strategy. All unrelated books remain unloaded. The authenticated account stream remains independent and continuously connected.
- The Yes and No tabs are real views of the same binary book: the No ladder is derived by complementing the synchronized Yes-price book. Clicking any bid or ask copies its exact side and price into the floating order slip. Submission follows the server's startup trading setting.
- The bottom activity dock remains fixed while the user searches, scrolls, or changes markets. Its collapsed bar always shows positions, working orders, unread fills, history, and last-fill state; it expands upward into the existing detail tables. Each new WebSocket fill produces a 12-second visual alert and unread count; snapshot/replayed fill IDs are suppressed.
- The board deliberately distinguishes `Not listed` (no safely matched Kalshi contract for that market type) from `Listed · no offer` (a mapped contract exists but has no usable ask). Neither state can open an order slip.
- Market discovery reruns independently every `PMBATTLE_MARKET_INTERVAL` (five minutes by default), reapplies saved mapping decisions, replaces stale board views, and publishes the refreshed schedule. A mutex prevents overlapping reconnect and timer refreshes.
- Attached live market views are copied into the unfiltered schedule before preference filtering. Saving sport or added-game preferences therefore cannot erase verified prices.
- Participant resolution uses Kalshi's outcome label before its broader subtitle, preventing both team names in the contract question from making an otherwise exact matchup ambiguous. Current aliases include Louisiana-Monroe/UL Monroe, Mississippi St./Mississippi State, and Hawai'i/Hawaii.
- Every quote carries its explicit Kalshi `yes`/`no` contract side. Selecting Away, Home, Over, or Under initializes the correct book side, while labels and four accessible colors remain consistent through the board, expanded book, and order slip.
- REST and WebSocket orders are decoded from Kalshi's current `*_dollars` and `*_count_fp` fields into fixed-point internal values. This is required for reliable remaining-quantity and cash-risk monitoring.
- Demo orders use Kalshi's current `/portfolio/events/orders` V2 shape. Buying NO is emitted as an ask at the complementary YES-book price. Counts and prices remain four-decimal fixed-point strings.
- `PMBATTLE_MAX_CASH_RISK` (dollars, 1 to 20000) lowers the per-order cash-risk ceiling for the whole server. The engine clamps anything outside that range back to the built-in $20,000 default, `Health.maxCashRisk` publishes the active value, the Settings safety grid shows it, and the order slip disables submission above it before any request is sent. A request above the cap that still reaches the engine returns `ErrCashRiskCap`.
- Parent sizing uses the conservative taker fee even for post-only orders. A binary search selects the greatest fractional contract quantity whose all-in cost does not exceed the cash-risk target; large low-price calculations use overflow-safe integer arithmetic.
- An iceberg persists every child ID, client ID, quantity, fill quantity, and lifecycle state. Only one nonterminal slice is exposed at once. Partial fills retain that slice; a full slice creates exactly one replacement capped by both the configured slice and remaining fee-inclusive risk.
- Refresh errors pause the parent, duplicate fill IDs never refresh again, cancellation skips terminal slices, and per-fill fee rounding can shrink the remaining parent quantity. If a partially filled working child would exceed remaining risk, the demo engine cancels it before publishing the fill. A late fill received after cancellation still updates filled risk but cannot restart or re-cancel the strategy.
- Follow creation ignores the browser's displayed price and reads the current synchronized server book. YES follows the highest YES bid; NO follows the complement of the lowest YES ask. Every child is post-only, so it cannot automatically cross the spread.
- A follow amendment occurs only when the same-side top price changes, remains inside the hard fee-adjusted moneyline cap, and the 750 ms cooldown has elapsed. Repricing resizes the total/max-fillable count within remaining cash risk, rotates the client order ID, persists the decision, and publishes parent/order state before the new book reaches the browser.
- A stale book changes the parent to `paused_stale` without mutating the exchange. A price beyond the cap changes it to `price_capped` while leaving the safer resting child in place. Fresh acceptable data resumes the parent; an exchange amend error changes it to `paused` for manual review.
- A generic `paused` follow never retries automatically. Resume first confirms a synchronized book and an active nonterminal child, clears the amendment cooldown, and feeds the current book through the normal fee-cap and remaining-risk path. Canceled, filled, non-follow, missing-child, and stale-book resumes are rejected.
- The Orders tray exposes a compact kill switch for all managed parents, the currently expanded game, each strategy, or Kalshi. The control is absent while trading is locked. Scoped cancellation filters only nonterminal PMBattle parents, calls the normal parent cancellation path, and reports partial failures instead of rolling back acknowledged cancels.
- Every fill carries its exchange order ID. The engine applies each fill ID once, updates filled quantity/risk, reduces the remaining reservation, persists the parent, and only then publishes the parent followed by the fill. Filled risk remains included in the station-wide cash-at-risk total.
- Startup and account-stream reconnects query Kalshi fill history by PMBattle child order ID and replay it oldest-first. The initial `account_snapshot` refresh is quiet so recovered fills do not look like new live alerts.
- The account snapshot also reads Kalshi's available balance in cents and converts it into PMBattle's four-decimal fixed-point bankroll without floating-point math.
- Kalshi documents `balance` as cash currently available for trading; it is not total equity. The UI therefore labels it Available to trade. While the account WebSocket is connected, PMBattle refreshes balance, resting orders, positions, settlements, and managed fill recovery every 30 seconds in addition to startup/reconnect reconciliation.
- Parent creation holds one process-wide submission lock. Before any exchange call, the full cash-risk target must fit the last authenticated available balance after subtracting unexposed commitments promised to active managed parents. On acknowledgement, the active child's reserved risk is immediately removed from local available cash; periodic REST reconciliation replaces that conservative estimate with Kalshi's authoritative value.
- This shared guard treats an iceberg's hidden future slices and a follow parent's unexposed remaining target as committed bankroll. Concurrent HTTP requests cannot both pass against the same cash, and insufficient requests return before `PlaceOrder` is called.
- The exchange-neutral routing planner is the first cross-exchange layer. It ranks eligible levels by fee-included implied probability, allocates without exceeding either venue cash or level liquidity, shares one venue's balance across all of its levels, and returns an explicit unallocated remainder when the target cannot be filled safely. Its fixture for a $5,000 target over three $2,000 venues produces $2,000 + $2,000 + $1,000 in best-price order.
- The compact `account_summary` browser event carries authoritative available cash, allocatable new-order capacity, and cash at risk after every order/position/strategy transition. Fill processing publishes this summary before the fill alert, preserving the risk-before-UI invariant.
- Account restart/reconnect reconciliation follows every `cursor` page for resting orders and nonzero, unsettled positions. Signed position quantities become explicit YES/NO sides, while exposure, traded amount, realized P&L, and fees remain fixed-point.
- Settled markets come from Kalshi's separate paginated settlement endpoint. PMBattle uses a one-second overlap on incremental reads, upserts by exchange/ticker in SQLite, retains the 500 latest records in the browser snapshot, and shows revenue, fees, and net P&L in History. Settlements are deliberately excluded from current cash at risk.
- Station cash at risk is the larger of (a) authenticated open-position exposure plus nonterminal order risk and (b) the managed parent engine's filled-plus-reserved total. This avoids double-counting managed children while remaining conservative during account-stream timing gaps.
- Kalshi sequence numbers are subscription-wide, not ticker-wide. A book-stream gap forces the consolidated UI/strategy subscription to reconnect and marks every cached member stale until fresh snapshots arrive.
- Sports preferences are stored in SQLite. No saved preference means all sports; saving an empty selection intentionally loads no sports.
- Extra/added games are identified by an exactly six-digit numeric schedule event ID. The Settings tab can exclude them before market matching and subscription.
- Simulated events include selectable moneyline, spread, and total quotes. Six-digit added games use lower simulated available quantities.

## Kalshi API verification status (September 1, 2026)

Kalshi's own documentation sites were unreachable from the remote coding environment, so the checks below used web search summaries and a third-party SDK's documentation (TexasCoding/kalshi-python-sdk). Treat "consistent" as "no contradiction found", not proof. The first live order must confirm each line.

- **Fee rates.** Taker `0.07 × C × P × (1−P)` and maker `0.0175 × C × P × (1−P)` match `internal/pricing`. Consistent.
- **Fee rounding.** Public summaries say "rounded up" and Kalshi's maker rebate policy implies rounding to the cent per trade; the code rounds up to $0.0001. If Kalshi rounds each fill to the next cent, a real fill can cost up to $0.0099 more than the reservation. Compare `fee_cost` on the first live fill with the app's fee column and adjust `pricing.KalshiFee` if they differ.
- **Create order V2.** Path `/portfolio/events/orders`, `side` is `bid`/`ask` on the YES book, `count` and `price` are fixed-point strings, `client_order_id` required, `time_in_force` values match, `self_trade_prevention_type` accepts `taker_at_cross`. Consistent. `post_only` and `cancel_order_on_pause` were not listed in the SDK documentation; whether the V2 endpoint honors `post_only` is unverified. Until a live test proves a post-only order is rejected rather than filled when it would cross, do not run follow orders live.
- **Amend order V2.** Path `/portfolio/events/orders/{id}/amend`; `count` is the total/max-fillable count (filled plus desired remaining), which matches the engine. The SDK documents the response as `{old_order, order}` where `order` may carry a **new order_id**. The client now decodes both the flat and nested shapes, and the follow engine adopts a replacement ID for later fills, cancels, and risk checks while keeping the old ID listed for late fills (`TestFollowTracksReplacementOrderIDAfterAmend`). `updated_client_order_id` was not in the SDK docs; if the exchange rejects unknown fields, the amend will fail closed and the parent pauses.
- **NO orders.** Buying NO at price p is sent as an ask on the YES book at 1−p. Standard for a single binary book. Consistent, unproven live.

## Known limitations

- Kalshi live order authentication and on-demand book streaming have been validated read-only against a production account. The September 1, 2026 refresh mapped 2,244 of 4,760 discovered contracts into 514 selectable game/strike books; a requested Clemson order book synchronized with full depth and the production order button remained disabled.
- The same live validation restored both moneyline sides for Clemson-LSU, Wisconsin-Notre Dame, UL Monroe-Mississippi State, UNLV-Hawaii, and UCLA-California. Of 86 enabled games, 63 currently have at least one mapped Kalshi moneyline; 23 have no mapped market. UCLA-California has no Kalshi spread or total event in the current catalog, so those cells correctly display `Not listed`.
- Initial league-to-series routing covers the major US leagues plus selected top soccer leagues. Add aliases as new schedule leagues are enabled; unknown leagues intentionally load no Kalshi series.
- The current general Kalshi fee rule is versioned in one module. Market-specific fee exceptions remain a known follow-up and must be verified during manual testing.
- Follow has automated coverage with a fake demo adapter and the current V2 amend contract, but it has not yet been manually exercised with separate Kalshi demo credentials. Production remains hard locked.
- The shared-bankroll gate is process-local and currently covers the single Kalshi adapter. The pure multi-exchange allocation planner is implemented and tested, but live routing still needs a second adapter plus the execution coordinator that creates children and resizes/cancels competing orders as fills arrive. Do not infer live smart routing from the planner alone.
- Completed parents are retained in SQLite without pruning yet. A retention policy will be needed as history grows.
- Audit records are append-only and paginated but do not yet have a retention/archive policy; server operators should include `pmbattle.db` in normal backups.
- Open-position and settlement restart reconciliation has recorded multi-page fixture coverage and was smoke-tested read-only against production on August 31, 2026: 7 open positions, 5 resting orders, and 50 recent settlements rendered successfully. The displayed $62,989.94 cash at risk exactly matched position exposure plus resting-order risk; trading remained disabled.
- The shared-bankroll/safety release was smoke-tested read-only against production on August 31, 2026: account state `READY`, 441 mapped markets, $213,074.64 available/new-order capacity, and $62,989.94 cash at risk rendered in Settings. The server and UI both reported `Production order entry is hard-locked`; no mutation endpoint was invoked.
- On-demand System audit was smoke-tested read-only on August 31, 2026: the bounded API returned newest-first cursor metadata, the browser loaded 15 existing records only after the subview was opened, and Details rendered the exact stored fixed-point fill payload. Production remained hard-locked and no mutation endpoint was invoked.
- Manual mapping review was smoke-tested read-only on August 31, 2026: 4,723 Kalshi contracts produced 2,240 confidently accepted mappings and zero current evidence-backed ambiguous groups. The on-demand API returned an empty list, Settings rendered the searchable `0 groups` empty state without browser errors, and the safety badge remained `READ-ONLY`. No accept/reject or exchange mutation endpoint was invoked.
- Production mutation is disabled by default and becomes available only when the server starts with production credentials, simulated mode off, and `PMBATTLE_TRADING_ENABLED=true`.
- The schedule feed is HTTP. Deploy through the office server and monitor its freshness; do not infer a game state when the feed is unavailable.

## Next milestone: manual Kalshi order validation

1. Start with a small manually selected production order and verify the request, acknowledgement, open-order display, cancellation, and fill monitoring against Kalshi.
2. Validate basic orders before testing iceberg or follow behavior; compare fees, partial fills, reconnect recovery, and risk totals against Kalshi's account display.
3. Keep `PMBATTLE_TRADING_ENABLED=false` whenever manual testing is not actively underway.

After a second exchange is selected, connect its normalized adapter to `internal/routing`, then add the fill-driven coordinator that reduces parent remaining risk before resizing or canceling competing venue children. Until then, the planner remains a tested, non-mutating foundation rather than an order path.

## Validation commands

```text
cd web && npm ci && npm run check && npm run build
go test ./...
go build .
```

GitHub Actions runs these checks and publishes portable Windows/Linux binaries on each push to `main`.

## Lightweight checkpoint

- Browser production bundle: about 88.5 KB JavaScript and 23.2 KB CSS before gzip; there are no runtime browser dependencies beyond Svelte. Mapping reviews stay out of the startup snapshot and live stream and load only when requested in Settings.
- Production source maps are disabled and old side-panel CSS/dead book code were removed.
- Runtime background work is bounded: one 30-second schedule ticker, one five-minute market-catalog ticker, one 30-second authenticated account-reconciliation ticker, one account stream, and one consolidated order-book stream containing only the selected UI book plus active follow books.
- The stripped single Windows executable is about 12.0 MB, primarily because it embeds the pure-Go SQLite implementation and the complete browser app; deployment still requires only that one executable.
- The catalog-refresh certification test covers new listings, health/schedule publication order, unfiltered preference state, and removal of withdrawn markets. Production HTTP tests cover create, individual cancel, scoped cancel, and resume locks plus same-origin streams and security headers. Current statement coverage is 53.4% for application orchestration and 54.0% for the HTTP server; the next target remains 70% through demo failure/reconnect cases.
