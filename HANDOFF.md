# PMBattle Engineering Handoff

## Current milestone

Milestone 1—the read-only terminal foundation—is implemented. Milestone 2 has basic, iceberg, and controlled follow orders. Limit, post-only, IOC basic orders, limit/post-only icebergs, and post-only follow parents flow through one durable cash-at-risk model into Kalshi's V2 order API. Parent state survives restart and reconciles order-scoped fills, paginated open positions, and settled-market history before the browser receives account activity. Order entry is disabled by default but can now be enabled for demo or production.

**Where the project actually stands.** The read-only terminal is proven against production. The order path is not: on September 1, 2026 the owner's first real order reached Kalshi and was **rejected** for count precision. That bug is fixed but the retry has not happened yet, so **no order has ever been accepted by Kalshi through PMBattle**. Everything about placing orders should still be treated as unproven.

The owner is not a programmer and places every order personally in the browser. No AI session may place, amend, cancel, or resume an order.

### Dedicated account workspaces: September 4, 2026

September 4 spread follow-up: Miami (FL)-Stanford, Fresno St.-USC, and Toledo-Michigan St. were present in Kalshi's public spread catalog but blank on PMBattle because the conservative matcher did not normalize Kalshi's parenthetical state codes or trailing `St.` abbreviation. `canonicalTeam` now maps Miami FL/OH explicitly and treats only a final `St.` token as `State`, preserving names such as St. Louis. Exact regression fixtures for all three affected games pass along with the full Go suite. Rebuild/restart is required before the running port-8080 executable shows the fix.

The former expandable bottom account tray has been replaced by dedicated /orders, /positions, /fills, and /history pages. A slim persistent monitor stays visible on every page, shows live counts and risk context, and opens each workspace with one click. The top navigation and browser Back/Forward controls use stable client-side URLs.

Orders has active/status, market, and strategy filters; summary risk; individual edit/cancel controls; scoped kill controls; and expandable identifiers, parent strategy data, children, and related fills. Positions shows fee-included American entry odds, risk, fees, P&L, contributing fills, and a direct market link. Fills shows recovered and live partial/full executions with raw price, fee-included odds, fee, risk, identifiers, bounded rendering, and direct market links. History separates settlements, completed parent strategies, and the on-demand paginated system audit.

The pages share one in-memory account snapshot and do not add polling, chart libraries, or frontend dependencies. The September 4 production bundle is 125.71 KB JavaScript and 33.41 KB CSS before gzip (41.56 KB and 6.54 KB gzipped). The frontend check completed with 0 errors and one accessibility warning on a keyboard-focusable completed-order row; the production build and full Go test suite passed. A separate simulated read-only executable was browser-tested on port 18080 in light/dark modes; direct routing, navigation, History rows, filters, and the persistent monitor worked. The production server on port 8080 was not restarted and no exchange mutation was sent.

Next account-workspace refinements: add server-backed date/sport/league filtering when histories outgrow the bounded snapshot, add CSV export, and add a retention/archive policy for completed parents and audit records. These are intentionally not on the live critical path yet.

### Reviewed local release: September 2, 2026

The September 2 usability/resilience batch is reviewed and ready. It adds keyboard-first board navigation, bounded schedule/fill rendering, automatic snapshot and WebSocket recovery, explicit browser connection state, stale-book action blocking, exact fee-inclusive cash sizing, maker/taker previews, iceberg input validation, clickable fills/history, scoped live-cancel confirmations, and a manual read-only account refresh in Settings.

Git history for this checkpoint: `9768f3d` contains the reviewed implementation, generated browser bundle, validation record, and permanent documentation updates. `c664032` records the deferred `exchange-aws` reference plan and explicitly keeps new venues out of the current milestone. These commits are intended to travel together on `main`.

The backend now applies the shared available-bankroll guard to basic/reconciled order amendments as well as new parent orders. An edit may reuse its current reservation but cannot consume cash reserved for other orders or hidden strategy commitments; a rejected edit never reaches Kalshi. Successful acknowledgements immediately adjust the displayed available balance until reconciliation supplies the authoritative account value.

Review evidence: `npm run check` completed with 0 errors and 0 warnings, the production frontend bundle built successfully, `go test ./...` passed under Go 1.26.5, and an embedded Windows review executable built successfully. A separate simulated, read-only server was browser-tested on port 18080: schedule filters, an expanded live book, clickable prices, conservative order sizing, Settings account refresh, reconnect indicators, and console output were checked without any exchange mutation. The owner's production server on port 8080 was not restarted or changed.

The final frontend bundle remains light: 106.30 KB JavaScript and 24.79 KB CSS before gzip (36.79 KB and 5.25 KB gzipped).

Order-book fee semantics: ask rows represent immediately crossing liquidity and display taker-fee all-in odds/cost. Bid rows represent joining the book post-only and display maker-fee all-in odds/cost. Parent cash reservation and the automatic hard price cap remain conservatively sized with the taker fee even when an order is intended to rest.

The order-book ladder identifies the owner's resting bid levels from the reconciled account stream. An exact ticker, contract-side, and limit-price match receives a `YOU` marker, high-contrast outline, and the summed remaining quantity when multiple active orders share that level. This is derived entirely from the existing account snapshot and adds no market-data subscription or polling work.

### Session log: September 1, 2026 (all merged to `main`, CI green)

Eleven commits, oldest first. `git log --oneline 99eb1c4..0adbc50` reproduces this list.

| Commit | What changed and why |
| --- | --- |
| `310299b` | Added `CLAUDE.md` so every AI session starts with the same rules: read the handoff, run the tests, never commit secrets, never enable trading, never place an order. |
| `4fbbefa` | Safety labels lied in production: Settings said demo mutations were on and production blocked, and the kill switch was labelled "Demo". Both now state the live mode. |
| `6819e90` | Added `PMBATTLE_MAX_CASH_RISK`, a per-order dollar ceiling (1–20000) that only lowers the built-in $20,000 limit. Enforced in the engine, published in health, blocked in the order slip. |
| `3662799` | Added a password sign-in and a cross-site request-forgery guard. |
| `b221907` | Kalshi's amend endpoint is documented as returning `{old_order, order}` where the new order may carry a **different order_id**. The client decodes both response shapes and the follow engine adopts the replacement ID for fills, cancels and risk, keeping the old ID listed for late fills. |
| `183a952` | Wrote `FIRST-LIVE-ORDER.md`, the owner's browser checklist for the first real order. |
| `3ebb4b6` | **Removed the password sign-in at the owner's request.** The request-header guard stayed. The network allowlist is the only access gate again. |
| `3917acc` | Merged the branch to `main` so a plain clone gets everything. |
| `270c08a` | **The live rejection fix.** See "Kalshi API verification status". |
| `5e0e7a2` | Saving preferences restarts the exchange loop, which caused `SQLITE_BUSY` and a blank Available figure. SQLite settings now travel on the connection string, an interrupted reconcile stays silent, and the restart reconciles the account before the slow market refresh. |
| `0adbc50` | Replaced raw Kalshi tickers in Positions/Orders/Fills/History with the game and the bet. See "Readable account rows". |

## Architecture

- `main.go` loads configuration, opens SQLite, starts the application service, embeds the frontend, and shuts down gracefully.
- `internal/schedule` downloads and normalizes the sportsbook XML feed.
- `internal/kalshi` implements environment selection, RSA-PSS authentication, market discovery, and separate account/order-book WebSocket subscriptions.
- `internal/mapping` conservatively matches exchange markets to canonical schedule events and supplies evidence-bounded candidates for manual review.
- `internal/pricing` calculates current Kalshi maker/taker fees and fee-adjusted American moneylines using fixed-point money.
- `internal/live` maintains sequence-checked in-memory order books.
- `internal/orders` validates and sizes fee-inclusive parent orders, enforces the moneyline cap, links child orders, and owns demo iceberg, follow, and cancellation state.
- `internal/routing` deterministically plans a single parent cash-risk target across exchange price levels using fee-included price, shared venue cash, hidden commitments, liquidity, freshness, and the hard all-in price cap. It does not execute orders.
- `internal/storage` owns SQLite WAL tables for events, automatic mappings, manual overrides, grouped review payloads, durable parent orders, settlements, market labels, settings, and audit history.
- `internal/app` coordinates polling, mapping, reconciliation, streaming, and the browser snapshot. `internal/app/labels.go` turns exchange tickers into readable game and outcome names.
- `internal/server` exposes JSON and WebSocket endpoints, with mutation handlers delegated to the startup-controlled service guard, and serves the embedded app.
- `web/src/App.svelte` contains the lightweight sportsbook board, instant search, filters, click-to-expand inline book ladder, and bottom activity tray.
- `web/src/orderslip.css` isolates the small floating order-slip surface from the critical board styles.
- `web/src/monitor.css` contains order-action and transient fill-alert styling; the unified fixed activity dock lives in the main board stylesheet.
- `web/src/sides.css` owns the shared Away/Home/Over/Under color system and labels.

## Browser API

- `GET /api/health` — service and feed state
- `GET /api/snapshot` — events, open positions, recent settlements, managed orders, bankroll, and health. Positions, orders, fills, and settlements each carry `game` and `outcome` (fills use `team` for the outcome) alongside `ticker`.
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

Every non-GET `/api` request must carry `X-Requested-With: PMBattle`, or it is rejected with 403 before any handler runs (`internal/server/guard.go`); the browser helper `api()` in `App.svelte` adds it to every call. There is no login: a password sign-in was built on September 1 and removed the same day at the owner's request, so the network allowlist is the only access gate.

Mutation routes are inert by default. Set `PMBATTLE_SIMULATED=false`, choose `PMBATTLE_KALSHI_ENV=demo` or `production`, and set `PMBATTLE_TRADING_ENABLED=true` to enable them. The Kalshi client receives the same startup flag and independently rejects place, amend, and cancel calls when it is off. Production submissions receive an explicit browser confirmation and are labeled `REAL ORDERS` / `LIVE TRADING`.

## Running the project

From a fresh clone, install and embed the browser app, then build the Go server:

```text
cd web
npm ci
npm run build
cd ..
go build -o pmbattle.exe .
```

**Rebuild both halves after every `git pull`.** The executable embeds `web/dist`, so skipping `npm run build` ships a stale browser page, and skipping `go build` runs stale server code. This has already caused confusion once.

Read-only is the normal way to run it. Windows PowerShell, with the owner's machine-local credentials:

```powershell
$env:PMBATTLE_KALSHI_ENV = "production"
$env:PMBATTLE_KALSHI_KEY_ID = "your-key-id"
$env:PMBATTLE_KALSHI_PRIVATE_KEY_PATH = "C:\secure\kalshi-private-key.pem"
$env:PMBATTLE_SIMULATED = "false"
$env:PMBATTLE_TRADING_ENABLED = "false"
.\pmbattle.exe
```

Only the owner arms order entry. On Windows, `start-live-100.bat` starts production trading across all mapped markets with `PMBATTLE_MAX_CASH_RISK=100`, loads credentials from `%USERPROFILE%\Desktop\kalshi.env`, and pauses before launch. Its `--check` option validates the executable and credential files without starting the server. It does not stop an existing server automatically, so port 8080 must be free first. `FIRST-LIVE-ORDER.md` provides the manual test checklist. No AI session runs the live launcher or sets the trading flag.

Open `http://127.0.0.1:8080/`. Credentials and private keys stay outside Git and must be configured separately on every computer. Linux uses the same environment names and `go build -o pmbattle .`; portable Windows/Linux artifacts are also published by GitHub Actions.

## Important operational details

- The office server's existing IP allowlist is the access boundary; anyone who can reach the port can place orders while trading is enabled. PMBattle should sit behind its normal TLS/reverse-proxy setup.
- The Settings safety panel is informational only and cannot arm trading. It surfaces environment, feed/account sync, last account refresh, mapped markets, available cash, cash at risk, and the immutable startup lock reason. When trading is enabled, its note and the Orders-tray kill switch label now say whether real production orders or demo orders are armed; an earlier version wrongly said production was still blocked.
- History separates exchange settlements from System audit. Audit records are never included in the startup snapshot or WebSocket stream; opening that subview requests the newest 100 records, and Load earlier follows the opaque numeric cursor. Each row has a compact lifecycle summary and a collapsible exact JSON payload.
- Use a dedicated server directory for `pmbattle.db` and back it up normally. `storage.Open` builds a `file:` connection string with `busy_timeout(5000)`, `journal_mode(WAL)`, and `_txlock=immediate`; keep those on the DSN, not as one-off PRAGMA statements, because `database/sql` pools connections and a PRAGMA only reaches the connection it ran on.
- Use the server's secret/environment manager for the Kalshi key ID and PEM path.
- Start with Kalshi demo. Confirm the health indicator and market mappings before considering production data.
- The Kalshi adapter requests only enabled schedule leagues and main `GAME`, `SPREAD`, and `TOTAL` event series. Multileg and prop catalogs are outside the main board path.
- The market matcher uses Kalshi's authoritative two-team event title plus occurrence time. Both participants must match; ambiguous duplicate matchups remain `review` and are hidden.
- Repeat MLB matchups on consecutive days are ordered by occurrence-time distance and auto-accepted only when the nearest schedule game is at least six hours clearer than the runner-up. This resolves the normal daily-series ambiguity that previously hid every baseball line while leaving close doubleheaders in manual review.
- The sportsbook board and date dropdown hide events before the browser's local calendar date, so retained schedule-feed rows from yesterday do not consume screen space. Opening an exact older ticker from an order or position temporarily exempts only that event so account navigation still works.
- The manual review queue groups contracts sharing exchange, Kalshi event title, and occurrence time so a single decision covers related tickers. Candidates require positive two-team evidence and a start time within 36 hours. A decision can select only one of those server-issued candidates; contracts with no safe candidate stay hidden and do not flood the queue.
- Accepted and rejected decisions are stored in a separate override table, survive catalog refreshes/restarts, are applied before tradable books are attached, and create an immutable `mapping_review_decided` audit record. Accepting or rejecting never calls an exchange mutation endpoint.
- Main spread and total lines are selected from the active strike closest to a 50% midpoint. Up to five nearby strikes are retained for the inline line selector.
- Clicking a game expands its order book in place. The consolidated book stream contains the selected UI ticker plus the unique tickers required by active follow parents; changing or closing the dropdown never stops a working follow strategy. All unrelated books remain unloaded. The authenticated account stream remains independent and continuously connected.
- The Yes and No tabs are real views of the same binary book: the No ladder is derived by complementing the synchronized Yes-price book. Clicking any bid or ask copies its exact side and price into the floating order slip. Submission follows the server's startup trading setting.
- The bottom activity dock remains fixed while the user searches, scrolls, or changes markets. Its collapsed bar always shows positions, working orders, unread fills, history, and last-fill state; it expands upward into the existing detail tables. Each new WebSocket fill produces a 12-second visual alert and unread count; snapshot/replayed fill IDs are suppressed.
- Positions, orders, fills, settlement history, and audit rows use a larger readability scale than the compact sportsbook board: 13px primary labels, 12px values, 11px secondary details, and 54px minimum rows. The expanded dock can use up to 44% of the viewport or 430px while remaining independently scrollable.
- Order and position rows are one-click market shortcuts. Mouse click or Enter/Space resolves the exact ticker across the event's main and alternate lines, clears schedule filters, collapses the dock, expands the mapped game, requests that contract's live book, and scrolls it into view. Unmapped legacy account rows remain inert rather than opening a guessed market; order action buttons stop propagation so cancel/resume never navigates accidentally.
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

## Readable account rows

A Kalshi ticker such as `KXNCAAFTOTAL-26SEP05ECUALA-53` is meaningless on screen, so every position, order, fill, and settlement carries a `game` ("#301 Clemson at #302 LSU") and an `outcome` ("Over 52.5"). `internal/app/labels.go` resolves them in three tiers, best first:

1. **The live board.** `findQuoteForSide` matches the ticker *and the contract side*, because a total's Over and Under share one ticker and differ only by side. Spread lines mirror for the home side, since `MarketView.Line` always describes the away side.
2. **Stored market labels.** Every catalog refresh saves each discovered market's title, yes/no outcome, type, and line into the `market_labels` table. For tickers that are neither on the board nor stored, `reconcileAccount` and `reconcileSettlements` call the read-only `Adapter.DescribeMarket`, at most 40 per cycle, and a failed lookup is not retried for an hour. This is what makes settled History rows readable long after the game is gone.
3. **The ticker itself,** decoded to "College Football · Sep 5 · ECUALA" and "Total". This tier deliberately never shows a line number: a 52.5 total is ticker strike 53, so guessing would put a wrong figure next to real money.

Settlement history is re-named on every load rather than only when new rows arrive, so rows stored before names existed become readable without a migration. The browser shows the game in bold, the outcome and market beneath, and keeps the raw ticker as the row's hover text.

## Kalshi API verification status (September 1, 2026)

Kalshi's own documentation sites were unreachable from the remote coding environment, so the checks below used web search summaries and a third-party SDK's documentation (TexasCoding/kalshi-python-sdk). Treat "consistent" as "no contradiction found", not proof. The first live order must confirm each line.

- **Fee rates.** Taker `0.07 × C × P × (1−P)` and maker `0.0175 × C × P × (1−P)` match `internal/pricing`. Consistent.
- **Fee rounding.** Public summaries say "rounded up" and Kalshi's maker rebate policy implies rounding to the cent per trade; the code rounds up to $0.0001. If Kalshi rounds each fill to the next cent, a real fill can cost up to $0.0099 more than the reservation. Compare `fee_cost` on the first live fill with the app's fee column and adjust `pricing.KalshiFee` if they differ.
- **Count precision: verified live on September 1, 2026.** The owner's first production order was rejected with `invalid count_fp: must be a fixed-point decimal string with 0-2 decimal places`. PMBattle had sent a four-decimal count. Fix: `QuantityForCashRisk` now rounds the sized quantity down to a whole `domain.ContractStep` (0.01 contract) so the all-in cost stays under the cash-risk target, and `fixed.FormatCount` sends `count` with exactly two decimals in both create and amend. Prices still use four decimals via `fixed.Format`. Tests: `TestQuantityForCashRiskUsesWholeHundredthsOfAContract`, `TestPlaceOrderSendsTwoDecimalCountAndFourDecimalPrice`, `TestFormatCountUsesTwoDecimalsAndRoundsDown`. Everything else below remains unproven until the next live attempt.
- **Create order V2.** Path `/portfolio/events/orders`, `side` is `bid`/`ask` on the YES book, `count` and `price` are fixed-point strings (count: 2 decimals, price: 4), `client_order_id` required, `time_in_force` values match, `self_trade_prevention_type` accepts `taker_at_cross`. The endpoint, authentication, and payload shape reached Kalshi's validator on September 1, which rejected only the count precision. Consistent. `post_only` and `cancel_order_on_pause` were not listed in the SDK documentation; whether the V2 endpoint honors `post_only` is unverified. Until a live test proves a post-only order is rejected rather than filled when it would cross, do not run follow orders live.
- **Amend order V2.** Path `/portfolio/events/orders/{id}/amend`; `count` is the total/max-fillable count (filled plus desired remaining), which matches the engine. The SDK documents the response as `{old_order, order}` where `order` may carry a **new order_id**. The client now decodes both the flat and nested shapes, and the follow engine adopts a replacement ID for later fills, cancels, and risk checks while keeping the old ID listed for late fills (`TestFollowTracksReplacementOrderIDAfterAmend`). `updated_client_order_id` was not in the SDK docs; if the exchange rejects unknown fields, the amend will fail closed and the parent pauses.
- **NO orders.** Buying NO at price p is sent as an ask on the YES book at 1−p. Standard for a single binary book. Consistent, unproven live.

## Known limitations

- Kalshi live order authentication and on-demand book streaming have been validated read-only against a production account. The September 1, 2026 refresh mapped 2,244 of 4,760 discovered contracts into 514 selectable game/strike books; a requested Clemson order book synchronized with full depth and the production order button remained disabled.
- The same live validation restored both moneyline sides for Clemson-LSU, Wisconsin-Notre Dame, UL Monroe-Mississippi State, UNLV-Hawaii, and UCLA-California. Of 86 enabled games, 63 currently have at least one mapped Kalshi moneyline; 23 have no mapped market. UCLA-California has no Kalshi spread or total event in the current catalog, so those cells correctly display `Not listed`.
- Initial league-to-series routing covers the major US leagues plus selected top soccer leagues. Add aliases as new schedule leagues are enabled; unknown leagues intentionally load no Kalshi series.
- The current general Kalshi fee rule is versioned in one module. Market-specific fee exceptions remain a known follow-up and must be verified during manual testing.
- Follow has automated coverage with a fake adapter, including replacement order IDs on amend, but it has never been exercised against Kalshi. It must not run live until the post-only and amend-response questions in "Kalshi API verification status" are settled. The owner has chosen to test in production with a small `PMBATTLE_MAX_CASH_RISK` rather than in the demo environment.
- The first production order attempt on September 1, 2026 reached Kalshi and was rejected for count precision (fixed the same day, see the verification section). No order has been accepted by Kalshi through PMBattle yet. `FIRST-LIVE-ORDER.md` is the script for the next attempt.
- Known, not fixed: in simulated mode the account reconcile overwrites the seeded demo Orders and Positions with the empty read-only snapshot, so those two tabs usually look empty while Fills keeps its seeded row. Pre-existing and only affects the simulated demo.
- Saving sport preferences restarts the exchange loop. On September 1 that produced `database is locked (SQLITE_BUSY)` in save schedule and a blank Available figure until the next 30-second refresh. Fixed the same day: SQLite's busy timeout, WAL mode, and immediate write locking are now set through the connection string so every pooled connection has them (`TestConcurrentWritersDoNotHitSQLiteBusy` fails on the old code), an interrupted reconcile no longer publishes partial state or marks the account degraded (`TestInterruptedReconciliationKeepsPriorAccountStateAndStaysSilent`), and the restarted loop reconciles the account before the slow market refresh. Not reproduced live yet; confirm on the next run by saving preferences while connected.
- The shared-bankroll gate is process-local and currently covers the single Kalshi adapter. The pure multi-exchange allocation planner is implemented and tested, but live routing still needs a second adapter plus the execution coordinator that creates children and resizes/cancels competing orders as fills arrive. Do not infer live smart routing from the planner alone.
- Completed parents are retained in SQLite without pruning yet. A retention policy will be needed as history grows.
- Audit records are append-only and paginated but do not yet have a retention/archive policy; server operators should include `pmbattle.db` in normal backups.
- Open-position and settlement restart reconciliation has recorded multi-page fixture coverage and was smoke-tested read-only against production on August 31, 2026: 7 open positions, 5 resting orders, and 50 recent settlements rendered successfully. The displayed $62,989.94 cash at risk exactly matched position exposure plus resting-order risk; trading remained disabled.
- The shared-bankroll/safety release was smoke-tested read-only against production on August 31, 2026: account state `READY`, 441 mapped markets, $213,074.64 available/new-order capacity, and $62,989.94 cash at risk rendered in Settings. The server and UI both reported `Production order entry is hard-locked`; no mutation endpoint was invoked.
- On-demand System audit was smoke-tested read-only on August 31, 2026: the bounded API returned newest-first cursor metadata, the browser loaded 15 existing records only after the subview was opened, and Details rendered the exact stored fixed-point fill payload. Production remained hard-locked and no mutation endpoint was invoked.
- Manual mapping review was smoke-tested read-only on August 31, 2026: 4,723 Kalshi contracts produced 2,240 confidently accepted mappings and zero current evidence-backed ambiguous groups. The on-demand API returned an empty list, Settings rendered the searchable `0 groups` empty state without browser errors, and the safety badge remained `READ-ONLY`. No accept/reject or exchange mutation endpoint was invoked.
- Production mutation is disabled by default and becomes available only when the server starts with production credentials, simulated mode off, and `PMBATTLE_TRADING_ENABLED=true`.
- The schedule feed is HTTP. Deploy through the office server and monitor its freshness; do not infer a game state when the feed is unavailable.

## Next milestone: retry the first live basic order

**This is the one thing that matters next.** The September 1 attempt was rejected for count precision; the fix is on `main` but unproven. The owner must rebuild before retrying, or they will run the old binary and see the same rejection.

The owner runs `FIRST-LIVE-ORDER.md` in the browser against production with `PMBATTLE_MAX_CASH_RISK=5`. The next session acts on what they report:

1. **If they have not yet rebuilt**, tell them first: `git pull`, then `cd web && npm run build && cd ..`, then `go build -o pmbattle.exe .`. The running executable embeds the browser page and the old code until it is rebuilt.
2. If Part 1 (read-only sanity) failed, fix that before anything else. Nothing downstream is trustworthy if the account panel is wrong.
3. If the order is rejected again, get the exact Kalshi error text. It is stored verbatim in the System audit tab under `parent order rejected`, and that record is the primary evidence, not the owner's description.
4. If Part 2 contract counts differed between PMBattle and Kalshi, compare `QuantityForCashRisk` sizing and the `count` string from `PlaceOrder` against the audit record for that order.
5. If Part 3 fees differed, adjust `pricing.KalshiFee` rounding to match Kalshi's rule (see "Kalshi API verification status") and add a test reproducing the owner's exact numbers.
6. Record the outcome, with dates and numbers, in "Known limitations", and delete the lines the live test has settled. Mark the count rule as proven only once an order is **accepted**.
7. Only after two clean basic orders: design a live test for iceberg (one slice, tiny size), then follow. Follow additionally needs proof that a post-only order that would cross is rejected, and that the amend response's order ID behaviour matches the replacement-ID handling.
8. Keep `PMBATTLE_TRADING_ENABLED=false` in every start script not actively under test.

### Backlog, in the owner's priority order

The owner's stated priorities for this project are **speed and legibility**. Proposed on September 1 and not yet chosen:

1. **Keyboard control.** The largest remaining speed win. `/` to focus search, `Esc` to close the order slip, arrow keys to move down the board, `Enter` to open a book. Everything needs the mouse today.
2. **Freeze the board header** so columns keep their meaning while scrolling a long list.
3. **Give the price more visual weight** than the exchange name and size inside each board cell; they currently compete.
4. **Mark games with no mapped market at the row level** instead of reading "Not listed" cell by cell.

Also open, lower priority: retention for completed parent orders and audit rows (both grow without bound), and the simulated-mode quirk in "Known limitations".

After a second exchange is selected, connect its normalized adapter to `internal/routing`, then add the fill-driven coordinator that reduces parent remaining risk before resizing or cancelling competing venue children. Until then, the planner remains a tested, non-mutating foundation rather than an order path.

### Deferred reference: `exchange-aws`

Do not add another venue yet. The owner's `C:\Users\David\Desktop\exchange-aws` project is reference material for a later multi-exchange milestone, after Kalshi basic-order submission, cancellation, fills, reconciliation, fee totals, and restart recovery have been proven.

When that milestone begins, extract and reimplement only the useful domain knowledge: ProphetX and 4Caster API behavior, rotation/team aliases, doubleheader and period matching, cancellation-during-submission handling, final-fill reconciliation after cancel, parent/child batch history, and cancel-and-reprice workflow. Preserve PMBattle's fixed-point accounting, centralized cash-risk authority, normalized `ExchangeAdapter`, stale-feed protection, audit trail, and lightweight single-server design.

Do not copy the legacy JSP/Tomcat structure, RabbitMQ as an in-process order engine, thread-per-order design, floating-point money calculations, hardcoded credentials or paths, silent league fallbacks, unauthenticated mutation endpoints, or independent venue bots without a shared bankroll. The reference folder contains exposed secrets and apparent key material; credentials must be rotated and the folder must not be published or imported into this repository.

## Validation commands

```text
cd web && npm ci && npm run check && npm run build
go test ./...
go build .
```

GitHub Actions runs these checks and publishes portable Windows/Linux binaries on each push to `main`.

## Lightweight checkpoint

- Browser production bundle (September 1, 2026): 92.0 KB JavaScript and 23.7 KB CSS before gzip, 32.2 KB and 5.0 KB gzipped; there are no runtime browser dependencies beyond Svelte. Mapping reviews stay out of the startup snapshot and live stream and load only when requested in Settings.
- Production source maps are disabled and old side-panel CSS/dead book code were removed.
- Runtime background work is bounded: one 30-second schedule ticker, one five-minute market-catalog ticker, one 30-second authenticated account-reconciliation ticker, one account stream, and one consolidated order-book stream containing only the selected UI book plus active follow books.
- The stripped single Windows executable is 12.3 MB, primarily because it embeds the pure-Go SQLite implementation and the complete browser app; deployment still requires only that one executable.
- 94 Go tests, all against a fake exchange adapter; no test ever sends a real order mutation. The catalog-refresh certification test covers new listings, health/schedule publication order, unfiltered preference state, and removal of withdrawn markets. Production HTTP tests cover create, individual cancel, scoped cancel, and resume locks plus the request-header guard, same-origin streams, and security headers.
- Statement coverage on September 1, 2026: app 56.9%, orders 73.4%, kalshi 52.9%, server 56.2%, storage 62.3%, mapping 88.8%. The next target remains 70% for app and server, through demo failure and reconnect cases.
- Two tests are written to fail against the pre-fix code and should stay that way: `TestConcurrentWritersDoNotHitSQLiteBusy` (storage) and `TestQuantityForCashRiskUsesWholeHundredthsOfAContract` (orders). If either is ever loosened, the bug it guards can return silently.
# 2026-09-01 live-order response compatibility

- Kalshi Create Order V2 now returns a flat acknowledgement (`order_id`, `client_order_id`, `fill_count`, `remaining_count`, `ts_ms`). PMBattle now accepts that current shape while retaining support for the older nested `order` response.
- Regression coverage is in `internal/kalshi/client_test.go`; the full Go test suite passes.
- During discovery, a real Pittsburgh Pirates order at 63 cents was confirmed resting through account reconciliation even though the old parser showed an error. Do not assume a create-order parsing error means an order was rejected; always reconcile before retrying.
- Active account orders now support individual cancellation through `DELETE /api/orders/{id}`. The service routes managed child orders through their parent strategy and directly cancels reconciled Kalshi orders that have no local parent. The UI asks for confirmation in live mode and exposes Cancel on every working order row.
- Kalshi account snapshots now request account-wide `/portfolio/fills?limit=1000`, so the Fills tray recovers executions from periods when PMBattle was offline. The app deduplicates them against WebSocket fills, enriches their game/rotation/market labels, and retains the newest 250.
- Positions now derive average entry price from current market exposure divided by absolute contracts and display American odds as raw → fee included using Kalshi-reported fees.
- `PATCH /api/orders/{id}` supports low-latency inline edits of remaining quantity and limit price for basic/reconciled orders. The server converts remaining quantity to Kalshi's total/max-fillable amend count, enforces the per-order risk cap, audits the request/result, updates parent state for basic orders, and refuses manual edits to iceberg/follow children.
- Browser submission is single-flight and displays `Checking Kalshi—do not resubmit`. If create-order acknowledgement is missing/ambiguous, the Kalshi adapter lists recent orders and matches the unique `client_order_id` before returning an error.
- `DELETE /api/orders` cancels every active account order, not only PMBattle parent children. The live UI confirms the number of active orders and reports acknowledged cancellations versus failures.
- Account-wide historical fills are now fetched with `limit=250` and merged in one quiet batch. Old fills no longer run individually through strategy, notification, or audit paths; managed parents still use the separate order-scoped recovery pass.
