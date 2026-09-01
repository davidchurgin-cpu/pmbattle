# PMBattle

PMBattle is a fast, lightweight sportsbook-style terminal for prediction markets. It uses rotation numbers and the supplied sportsbook schedule as its event directory, then maps prediction-market contracts onto those games.

The production connection is deliberately **read-only**. The terminal includes the live schedule, Kalshi market discovery and WebSocket plumbing, fee-adjusted American moneylines, live order books, fills, positions, orders, instant search, sport/date/league filters, and a compact light/dark interface. Disabled-by-default basic, iceberg, and controlled follow paths exist only for authenticated Kalshi demo testing; real-money order entry is blocked in both startup configuration and the exchange client.

## What works now

- Schedule ingestion from `rawschedule_v2_expanded.xml` every 30 seconds
- Normalized sports, leagues, games, rotation numbers, teams, start times, and scores
- Conservative Kalshi market matching with uncertain matches hidden from the board
- Fixed-point money and fee calculations; no floating point is used for order cost or fees
- Fee-adjusted American moneylines as the primary displayed price
- Moneyline, spread, and total columns in the simulated sportsbook board
- Kalshi demo/production endpoints, RSA-PSS request signing, and authenticated WebSocket connection
- Click-to-expand, full-depth inline order-book ladders with bids, asks, contracts, cash totals, and raw-to-fee-included moneylines
- On-demand order-book subscriptions: PMBattle loads the selected market plus only the books required by active follow parents, releasing all other books
- Clickable Yes/No bid and ask levels that populate a floating bottom order slip with the exact price, cash-at-risk field, fee-adjusted cap, and basic/iceberg/follow controls
- Demo-only basic limit, post-only, IOC, and cancel commands sized from a parent cash-risk target with a hard fee-adjusted moneyline cap
- Demo-only iceberg parents that expose one configurable slice, refresh only after the active slice is completely filled, and cancel only the currently working slice
- Demo-only follow parents that join the live same-side top bid, stay post-only, never cross automatically, resize within cash risk, pause on stale data or the fee-adjusted cap, and throttle queue-losing amendments
- Demo-only kill switch scoped to the current event, strategy, Kalshi-managed parents, or every managed parent; partial cancellation failures are reported individually
- Manual demo-only Resume action for follow parents paused by an amend error; it requires a fresh synchronized book and reruns the fee cap and cash-risk checks before retrying
- Current Kalshi V2 amend requests use total/max-fillable count semantics and rotate the client order ID on every acknowledged reprice
- Current Kalshi V2 fixed-point order requests, including correct NO-to-YES-book conversion and idempotent client order IDs
- Durable parent orders that survive restart, deduplicate fills, and reduce remaining risk before fill notifications reach the browser
- Order-scoped REST fill recovery after startup and account-stream reconnects, plus the authenticated read-only available bankroll
- Paginated REST recovery of every open Kalshi position and settled market, with fixed-point exposure, fees, realized P&L, and recent settlement history persisted across restarts
- A live Positions view for unsettled exposure and a separate History view for up to 500 recent settlements; settled markets never count as current cash at risk
- Dashboard cash at risk includes authenticated open-position exposure plus resting-order risk, with the managed parent total used as a conservative fallback during feed timing gaps
- One shared available-bankroll guard serializes parent submissions and reserves each parent's full future cash-risk target, including hidden iceberg slices; an insufficient request is rejected before any exchange call
- Available cash, new-order capacity, and cash at risk update in the browser before a live fill notification is displayed
- A compact Settings safety panel shows environment, exchange/account state, last reconciliation, mapped-market count, available trading balance, cash at risk, and the server-controlled order-entry lock
- Always-visible dashboard order monitor with working quantities and recent fills, plus immediate visual alerts for every newly streamed full or partial fill
- Explicit, accessible side identities across the board, book, and slip: Away blue, Home purple, Over green, and Under amber
- Sequence-checked in-memory order books with stale-book detection
- Live browser stream for books, health, fills, orders, positions, and reconciled settlement history
- SQLite WAL persistence for schedules, mappings, settings, settlements, parent orders, and audit records
- Standard light and dark themes
- Persisted Settings tab for enabling only the sports you want to load and subscribe to
- Optional filter for six-digit extra/added games with lower market limits
- Windows, Linux, and Docker builds

## Quick start

### Run the prebuilt application

1. Download the appropriate executable from the latest GitHub Actions build.
2. Copy `.env.example` to `.env` and set your server values.
3. Keep `PMBATTLE_SIMULATED=true` until Kalshi credentials are configured.
4. Start the executable and open `http://SERVER-IP:8080` through the office server's existing IP allowlist.

The application reads environment variables from the server environment. It does not automatically read `.env`; use the server's normal service configuration or load the file before starting it.

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

Create an API key in the matching Kalshi environment and store the private key on the server, outside this repository.

```text
PMBATTLE_KALSHI_ENV=demo
PMBATTLE_KALSHI_KEY_ID=your-key-id
PMBATTLE_KALSHI_PRIVATE_KEY_PATH=/secure/path/kalshi-private-key.pem
PMBATTLE_SIMULATED=false
PMBATTLE_TRADING_ENABLED=false
```

Demo and production credentials are different. Never commit private keys, `.env`, databases, or server secrets. `PMBATTLE_TRADING_ENABLED` defaults to `false`; if it is set to `true` outside authenticated, non-simulated Kalshi demo mode, PMBattle refuses to start. The Kalshi adapter independently rejects place, amend, and cancel mutations unless its configured environment is `demo`.

### Using another PC

GitHub carries the application code, but deliberately does not carry credentials. On each PC or server:

1. Copy the Kalshi key ID and PEM private key through a secure channel, not through GitHub.
2. Store them outside the cloned PMBattle folder.
3. Set `PMBATTLE_KALSHI_KEY_ID` and `PMBATTLE_KALSHI_PRIVATE_KEY_PATH` for that machine.
4. Set `PMBATTLE_KALSHI_ENV=production` only for production keys; demo keys require `demo`.

The same Kalshi API key can authenticate from another PC if Kalshi account policy permits it, but the local files and environment settings do not transfer automatically.

## Safety model

- Uncertain market mappings are not displayed as tradable markets.
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
- The server enforces same-origin browser WebSockets and basic security headers.
- The production API and exchange adapter cannot place, amend, or cancel orders. PMBattle must not send any real-money order action without the user's explicit permission at that time.
- Demo order submission remains off unless `PMBATTLE_TRADING_ENABLED=true` is deliberately supplied with demo credentials. The engine rejects stale books, unknown mappings, invalid sides, requests above $20,000 cash risk, unsupported strategies, and prices beyond the fee-adjusted cap.
- Parent creation is serialized under one server lock. The full parent target must fit available Kalshi cash after subtracting unexposed commitments already reserved for managed iceberg/follow parents; a rejected target never reaches the adapter.

See [HANDOFF.md](HANDOFF.md) for architecture, operational details, known limitations, and the next implementation milestone.

