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
- The Kalshi adapter requests only enabled schedule leagues and main `GAME`, `SPREAD`, and `TOTAL` event series. Multileg and prop catalogs are outside the main board path.
- The market matcher uses Kalshi's authoritative two-team event title plus occurrence time. Both participants must match; ambiguous duplicate matchups remain `review` and are hidden.
- Main spread and total lines are selected from the active strike closest to a 50% midpoint, then displayed as both binary sides using fee-adjusted American odds.
- Kalshi sequence numbers are subscription-wide, not ticker-wide. A subscription gap forces reconnect and marks all cached books stale until new snapshots arrive.
- Sports preferences are stored in SQLite. No saved preference means all sports; saving an empty selection intentionally loads no sports.
- Extra/added games are identified by an exactly six-digit numeric schedule event ID. The Settings tab can exclude them before market matching and subscription.
- Simulated events include selectable moneyline, spread, and total quotes. Six-digit added games use lower simulated available quantities.

## Known limitations

- Kalshi live order authentication and book streaming have been validated read-only against a production account. Position/fill historical REST reconciliation remains the next account-data task.
- Initial league-to-series routing covers the major US leagues plus selected top soccer leagues. Add aliases as new schedule leagues are enabled; unknown leagues intentionally load no Kalshi series.
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
