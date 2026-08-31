# PMBattle Engineering Handoff

## Current milestone

Milestone 1—the read-only terminal foundation—is implemented. The repository builds a Svelte frontend into a single Go executable. Simulated mode is the safe default so the product can be reviewed before Kalshi credentials are added.

## Architecture

- `main.go` loads configuration, opens SQLite, starts the application service, embeds the frontend, and shuts down gracefully.
- `internal/schedule` downloads and normalizes the sportsbook XML feed.
- `internal/kalshi` implements environment selection, RSA-PSS authentication, market discovery, and WebSocket subscription.
- `internal/mapping` conservatively matches exchange markets to canonical schedule events.
- `internal/pricing` calculates current Kalshi maker/taker fees and fee-adjusted American moneylines using fixed-point money.
- `internal/live` maintains sequence-checked in-memory order books.
- `internal/storage` owns SQLite WAL tables for events, mappings, settings, and audit history.
- `internal/app` coordinates polling, mapping, reconciliation, streaming, and the browser snapshot.
- `internal/server` exposes read-only JSON and WebSocket endpoints and serves the embedded app.
- `web/src/App.svelte` contains the lightweight sportsbook board, instant search, filters, book panel, and bottom activity tray.

## Browser API

- `GET /api/health` — service and feed state
- `GET /api/snapshot` — events, account state, bankroll, and health
- `GET /api/books/{ticker}` — current in-memory order book
- `GET /api/settings` — available sports, event counts, and saved preferences
- `PUT /api/settings` — save enabled sports and refresh the schedule and exchange subscriptions
- `GET /api/ws` — compact live events: `schedule`, `health`, `ticker`, `orderbook`, `book_stale`, `fill`, `order`, `position`, and `market_lifecycle`

There are intentionally no mutation or trading endpoints in Milestone 1.

## Important operational details

- The office server's existing IP allowlist remains the access boundary. PMBattle should sit behind its normal TLS/reverse-proxy setup.
- Use a dedicated server directory for `pmbattle.db` and back it up normally.
- Use the server's secret/environment manager for the Kalshi key ID and PEM path.
- Start with Kalshi demo. Confirm the health indicator and market mappings before considering production data.
- The current market matcher requires at least three meaningful team-name token matches. Anything below that remains `review` and is excluded from the main board.
- If a book delta sequence skips, the book becomes stale and the UI displays the warning. A fresh snapshot is required before it is considered synchronized.
- Sports preferences are stored in SQLite. No saved preference means all sports; saving an empty selection intentionally loads no sports.
- Extra/added games are identified by an exactly six-digit numeric schedule event ID. The Settings tab can exclude them before market matching and subscription.
- Simulated events include selectable moneyline, spread, and total quotes. Six-digit added games use lower simulated available quantities.

## Known limitations

- Kalshi live fill/order/position payloads are normalized into PMBattle records, but they still need validation against credentials from the intended account. Historical REST reconciliation is the next account-data task.
- Market classification currently promotes the highest-confidence accepted Kalshi match as a moneyline view. Spread/total classification is the next mapping improvement.
- The current general Kalshi fee rule is versioned in one module, but market-specific fee exceptions must be added before any production order preview.
- The UI is intentionally read-only and contains no order form.
- The schedule feed is HTTP. Deploy through the office server and monitor its freshness; do not infer a game state when the feed is unavailable.

## Next milestone: Kalshi demo trading

1. Add normalized Kalshi order, fill, balance, and position decoders with recorded fixtures.
2. Add a disabled-by-default trading API protected by environment and visible demo/production gating.
3. Implement a parent-order state machine for basic limit, post-only, IOC, cancel, and replace.
4. Add cash-at-risk sizing and a hard fee-adjusted moneyline cap.
5. Add iceberg slicing and throttled join-the-top behavior without automatic spread crossing.
6. Process fills before UI publication and reconcile on every reconnect/restart.
7. Add event, strategy, exchange, and global cancel controls.
8. Keep production blocked until demo fees, partial fills, reconnect recovery, and risk totals match Kalshi reports.

## Validation commands

```text
cd web && npm ci && npm run check && npm run build
go test ./...
go build .
```

GitHub Actions runs these checks and publishes portable Windows/Linux binaries on each push to `main`.
