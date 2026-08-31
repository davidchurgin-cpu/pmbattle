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

## Safety model

- Uncertain market mappings are not displayed as tradable markets.
- Missing WebSocket sequence numbers mark the affected book stale.
- The UI clearly identifies simulated/live mode and stale data.
- Credentials never pass through the browser.
- The server enforces same-origin browser WebSockets and basic security headers.
- No trading endpoint exists yet.

See [HANDOFF.md](HANDOFF.md) for architecture, operational details, known limitations, and the next implementation milestone.

