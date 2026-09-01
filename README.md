# PMBattle

PMBattle is a fast, lightweight sportsbook-style terminal for prediction markets. It uses rotation numbers and the supplied sportsbook schedule as its event directory, then maps prediction-market contracts onto those games.

Order entry is disabled by default. The terminal includes the live schedule, Kalshi market discovery and WebSocket plumbing, fee-adjusted American moneylines, live order books, fills, positions, orders, instant search, sport/date/league filters, and a compact light/dark interface. Basic, iceberg, and controlled follow orders can be enabled for either Kalshi demo or production.

## What works now

- Schedule ingestion from `rawschedule_v2_expanded.xml` every 30 seconds
- Normalized sports, leagues, games, rotation numbers, teams, start times, and scores
- Conservative Kalshi market matching with uncertain matches hidden from the board
- Automatic Kalshi catalog refresh every five minutes so newly listed markets appear without a server restart
- On-demand Settings review for ambiguous mappings, grouped by related Kalshi contracts; approvals are limited to evidence-backed schedule candidates and rejections persist across refreshes
- Fixed-point money and fee calculations; no floating point is used for order cost or fees
- Fee-adjusted American moneylines as the primary displayed price
- Moneyline, spread, and total columns in the simulated sportsbook board
- Kalshi demo/production endpoints, RSA-PSS request signing, and authenticated WebSocket connection
- Click-to-expand, full-depth inline order-book ladders with bids, asks, contracts, cash totals, and raw-to-fee-included moneylines
- On-demand order-book subscriptions: PMBattle loads the selected market plus only the books required by active follow parents, releasing all other books
- Clickable Yes/No bid and ask levels that populate a floating bottom order slip with the exact price, cash-at-risk field, fee-adjusted cap, and basic/iceberg/follow controls
- Demo-only basic limit, post-only, IOC, and cancel commands sized from a parent cash-risk target with a hard fee-adjusted moneyline cap
- Iceberg parents that expose one configurable slice, refresh only after the active slice is completely filled, and cancel only the currently working slice
- Follow parents that join the live same-side top bid, stay post-only, never cross automatically, resize within cash risk, pause on stale data or the fee-adjusted cap, and throttle queue-losing amendments
- Kill switch scoped to the current event, strategy, Kalshi-managed parents, or every managed parent; partial cancellation failures are reported individually
- Manual Resume action for follow parents paused by an amend error; it requires a fresh synchronized book and reruns the fee cap and cash-risk checks before retrying
- Current Kalshi V2 amend requests use total/max-fillable count semantics and rotate the client order ID on every acknowledged reprice
- Current Kalshi V2 fixed-point order requests, including correct NO-to-YES-book conversion and idempotent client order IDs
- Durable parent orders that survive restart, deduplicate fills, and reduce remaining risk before fill notifications reach the browser
- Order-scoped REST fill recovery after startup and account-stream reconnects, plus the authenticated read-only available bankroll
- Paginated REST recovery of every open Kalshi position and settled market, with fixed-point exposure, fees, realized P&L, and recent settlement history persisted across restarts
- A live Positions view for unsettled exposure and a separate History view for up to 500 recent settlements; settled markets never count as current cash at risk
- Dashboard cash at risk includes authenticated open-position exposure plus resting-order risk, with the managed parent total used as a conservative fallback during feed timing gaps
- One shared available-bankroll guard serializes parent submissions and reserves each parent's full future cash-risk target, including hidden iceberg slices; an insufficient request is rejected before any exchange call
- Exchange-neutral smart-routing planner allocates one parent cash-risk target across venue balances and fee-included liquidity in best-price order, shares each venue's bankroll across its levels, and reports any safely unallocated remainder
- Available cash, new-order capacity, and cash at risk update in the browser before a live fill notification is displayed
- A compact Settings safety panel shows environment, exchange/account state, last reconciliation, mapped-market count, available trading balance, cash at risk, and the server-controlled order-entry lock
- Positions, Orders, Fills, and History name every row by game and bet ("#301 Clemson at #302 LSU", "Over 52.5") instead of the exchange's ticker code, with the ticker kept as hover text; settled markets stay readable because their names are stored locally
- Order status shown as a colour-coded chip, fills timed as "3m ago", and every money column right-aligned on shared digit widths for fast scanning
- Fixed bottom activity dock with always-visible position/order/fill counts, working-order status, and expandable detail tables, plus immediate visual alerts for every newly streamed full or partial fill
- Compact full-width sportsbook rows; genuinely absent market types say `Not listed`, while listed contracts without an offer say `Listed · no offer`
- Explicit, accessible side identities across the board, book, and slip: Away blue, Home purple, Over green, and Under amber
- Sequence-checked in-memory order books with stale-book detection
- Live browser stream for books, health, fills, orders, positions, and reconciled settlement history
- SQLite WAL persistence for schedules, automatic mappings, manual mapping overrides, review queues, settings, settlements, parent orders, and audit records
- On-demand System audit history for order requests, acknowledgements, rejections, fills, risk reconciliation, follow decisions, resumes, and cancellations; cursor paging keeps it out of the live snapshot
- Standard light and dark themes
- Persisted Settings tab for enabling only the sports you want to load and subscribe to
- Optional filter for six-digit extra/added games with lower market limits
- A request-header check that blocks cross-site request forgery on every state-changing call
- `PMBATTLE_MAX_CASH_RISK` per-order cash-risk cap for live testing, shown in Settings and enforced by both the order slip and the engine
- Windows, Linux, and Docker builds

## Quick start

### Run the prebuilt application

1. Download the appropriate executable from the latest GitHub Actions build.
2. Copy `.env.example` to `.env` and set your server values.
3. Keep `PMBATTLE_SIMULATED=true` until Kalshi credentials are configured.
4. Start the executable and open `http://SERVER-IP:8080` through the office server's existing IP allowlist.

The application reads environment variables from the server environment. It does not automatically read `.env`; use the server's normal service configuration or load the file before starting it.

`PMBATTLE_MARKET_INTERVAL` controls full Kalshi catalog discovery and defaults to `5m`; live prices and account activity continue to use WebSockets.

### Build from source

Requirements: Go 1.26.5+, Node.js 22+, and npm 10+.

Windows:

```powershell
./scripts/build.ps1
```

Linux:

```sh
./scripts/build.sh
```

The build creates one Windows executable and one Linux executable under `dist/`. The Svelte frontend is embedded into each executable.

### Docker

```sh
docker build -t pmbattle .
docker run --rm -p 8080:8080 --env-file .env -v pmbattle-data:/app/data pmbattle
```

Set `PMBATTLE_DB=/app/data/pmbattle.db` when using the volume above.

## Kalshi configuration

PMBattle uses Kalshi's V2 order endpoint and reconciles account orders after submission. If an order submission ever reports an ambiguous response, check the Orders tray or Kalshi directly before retrying so a live order is not duplicated.

Create an API key in the matching Kalshi environment and store the private key on the server, outside this repository.

```text
PMBATTLE_KALSHI_ENV=demo
PMBATTLE_KALSHI_KEY_ID=your-key-id
PMBATTLE_KALSHI_PRIVATE_KEY_PATH=/secure/path/kalshi-private-key.pem
PMBATTLE_SIMULATED=false
PMBATTLE_TRADING_ENABLED=false
```

`PMBATTLE_MAX_CASH_RISK` is an optional per-order ceiling in dollars, between 1 and 20000. While testing real orders, set it to a small number such as `5`; the server then refuses any single order with more cash at risk, the order slip disables its button above that amount, and the Settings safety panel shows the active cap. It can lower the built-in $20,000 limit but never raise it.

Demo and production credentials are different. Never commit private keys, `.env`, databases, or server secrets. `PMBATTLE_TRADING_ENABLED` defaults to `false`. To test real orders manually, use production credentials with `PMBATTLE_KALSHI_ENV=production`, `PMBATTLE_SIMULATED=false`, and `PMBATTLE_TRADING_ENABLED=true`, then restart PMBattle. The interface clearly labels real-order mode and asks for confirmation before each production submission. The adapter still rejects mutations unless trading was enabled at startup. Follow [FIRST-LIVE-ORDER.md](FIRST-LIVE-ORDER.md) for the first real order; it walks through a small order that is canceled before it fills, then one that fills, and how to lock trading again.

On Windows, `start-live-100.bat` is the owner-run launcher for production trading across all mapped markets with a hard `$100` cash-risk cap per order. It reads `api key.txt` and `private key.txt` from `%USERPROFILE%\Desktop\kalshi.env` by default and never stores secret values in Git. Run `start-live-100.bat --check` to validate the files without starting PMBattle. Stop any existing PMBattle server on port 8080 before double-clicking the launcher.

### Using another PC

GitHub carries the application code, but deliberately does not carry credentials. On each PC or server:

1. Copy the Kalshi key ID and PEM private key through a secure channel, not through GitHub.
2. Store them outside the cloned PMBattle folder.
3. Set `PMBATTLE_KALSHI_KEY_ID` and `PMBATTLE_KALSHI_PRIVATE_KEY_PATH` for that machine.
4. Set `PMBATTLE_KALSHI_ENV=production` only for production keys; demo keys require `demo`.

The same Kalshi API key can authenticate from another PC if Kalshi account policy permits it, but the local files and environment settings do not transfer automatically.

## Safety model

- Uncertain market mappings are not displayed as tradable markets.
- Manual mapping decisions change only PMBattle's local mapping state. They cannot place an order; a candidate must contain both matching teams within 36 hours of the Kalshi occurrence time, and contracts without a safe candidate remain hidden outside the review queue.
- WebSocket sequence gaps are validated at Kalshi's subscription level; a gap reconnects the feed and marks cached books stale until fresh snapshots arrive.
- Account activity stays connected independently from the on-demand order book, so orders and fills remain live while no game is expanded.
- Historical fills load quietly at startup; only new authenticated fill events create alerts, preventing notification spam after reconnects.
- A partial fill remains counted as cash at risk while its open-order reservation is reduced; canceling the remainder does not erase the risk already held in the resulting position.
- Iceberg refresh failures pause the parent without creating a phantom child. Duplicate fills cannot refresh twice, and fee-rounding changes shrink future quantity before allowing the parent to exceed its cash-risk target.
- Follow decisions are driven by the synchronized server book, not a browser-supplied price. A 750 ms amendment cooldown limits queue loss; active follow books stay subscribed when their game dropdown is closed.
- A generic follow error remains manually paused indefinitely. Resume is never automatic and is rejected for stale books, terminal parents, non-follow strategies, missing active children, and every production connection.
- Bulk cancellation reuses the guarded parent-order path, persists and publishes each acknowledgement immediately, and never claims full success when one child cancellation fails.
- The UI clearly identifies simulated/live mode and stale data.
- Kalshi's `balance` is treated as available trading cash, not total account equity. Account orders, positions, settlements, and available cash are reconciled every 30 seconds while connected, as well as at startup and after reconnects.
- Credentials never pass through the browser.
- Every state-changing API call must carry the `X-Requested-With: PMBattle` header, which a cross-site page cannot forge without a CORS preflight the server never grants. There is no login; the office network allowlist is the only access gate.
- The server enforces same-origin browser WebSockets, a restrictive content security policy, MIME sniffing protection, referrer suppression, and frame denial.
- Order submission remains off unless `PMBATTLE_TRADING_ENABLED=true` is deliberately supplied with authenticated demo or production credentials. The engine rejects stale books, unknown mappings, invalid sides, requests above the per-order cash-risk cap (default $20,000, lowered with `PMBATTLE_MAX_CASH_RISK`), unsupported strategies, and prices beyond the fee-adjusted cap.
- Parent creation is serialized under one server lock. The full parent target must fit available Kalshi cash after subtracting unexposed commitments already reserved for managed iceberg/follow parents; a rejected target never reaches the adapter.
- The routing planner uses fixed-point cash risk, fee-included American moneylines, per-venue available cash, hidden commitments, liquidity capacity, freshness, and the hard parent price cap. It is execution-independent and cannot send an order.

See [HANDOFF.md](HANDOFF.md) for architecture, operational details, known limitations, and the next implementation milestone.

