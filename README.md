# PMBattle

PMBattle is a fast, lightweight sportsbook-style terminal for prediction markets. It uses rotation numbers and the supplied sportsbook schedule as its event directory, then maps prediction-market contracts onto those games.

The current release is deliberately **read-only**. It includes the live schedule, Kalshi market discovery and WebSocket plumbing, fee-adjusted American moneylines, live order books, fills, positions, orders, instant search, sport/date/league filters, and a compact light/dark interface. Real-money order entry is disabled.

## What works now

- Schedule ingestion from `rawschedule_v2_expanded.xml` every 30 seconds
- Normalized sports, leagues, games, rotation numbers, teams, start times, and scores
- Conservative Kalshi market matching with uncertain matches hidden from the board
- Fixed-point money and fee calculations; no floating point is used for order cost or fees
- Fee-adjusted American moneylines as the primary displayed price
- Moneyline, spread, and total columns in the simulated sportsbook board
- Kalshi demo/production endpoints, RSA-PSS request signing, and authenticated WebSocket connection
- Click-to-expand, full-depth inline order-book ladders with bids, asks, contracts, cash totals, and raw-to-fee-included moneylines
- On-demand order-book subscriptions: PMBattle loads only the selected market, releases it when closed, and never streams every game unnecessarily
- Clickable Yes/No bid and ask levels that populate a floating bottom order slip with the exact price, cash-at-risk field, fee-adjusted cap, and basic/iceberg/follow controls
- Always-visible dashboard order monitor with working quantities and recent fills, plus immediate visual alerts for every newly streamed full or partial fill
- Sequence-checked in-memory order books with stale-book detection
- Live browser stream for books, health, fills, orders, and positions
- SQLite WAL persistence for schedules, mappings, settings, and audit records
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
PMBATTLE_ENABLE_TRADING=false
```

Demo and production credentials are different. Never commit private keys, `.env`, databases, or server secrets. Production trading remains disabled in this release even if `PMBATTLE_ENABLE_TRADING` is set.

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
- The UI clearly identifies simulated/live mode and stale data.
- Credentials never pass through the browser.
- The server enforces same-origin browser WebSockets and basic security headers.
- No trading endpoint exists yet.
- The floating order slip is a reviewed interface preview only; its submit button remains locked until the guarded demo parent-order engine is implemented.

See [HANDOFF.md](HANDOFF.md) for architecture, operational details, known limitations, and the next implementation milestone.

