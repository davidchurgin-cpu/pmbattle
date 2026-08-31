# PMBattle Engineering Handoff

## Current milestone

Milestone 1—the read-only terminal foundation—is implemented. The repository builds a Svelte frontend into a single Go executable. Simulated mode is the safe default so the product can be reviewed before Kalshi credentials are added.

## Architecture

- `main.go` loads configuration, opens SQLite, starts the application service, embeds the frontend, and shuts down gracefully.
- `internal/schedule` downloads and normalizes the sportsbook XML feed.
- `internal/kalshi` implements environment selection, RSA-PSS authentication, market discovery, and separate account/order-book WebSocket subscriptions.
- `internal/mapping` conservatively matches exchange markets to canonical schedule events.
- `internal/pricing` calculates current Kalshi maker/taker fees and fee-adjusted American moneylines using fixed-point money.
- `internal/live` maintains sequence-checked in-memory order books.
- `internal/storage` owns SQLite WAL tables for events, mappings, settings, and audit history.
- `internal/app` coordinates polling, mapping, reconciliation, streaming, and the browser snapshot.
- `internal/server` exposes read-only JSON and WebSocket endpoints and serves the embedded app.
- `web/src/App.svelte` contains the lightweight sportsbook board, instant search, filters, click-to-expand inline book ladder, and bottom activity tray.
- `web/src/orderslip.css` isolates the small floating order-slip surface from the critical board styles.

## Browser API

- `GET /api/health` — service and feed state
- `GET /api/snapshot` — events, account state, bankroll, and health
- `GET /api/books/{ticker}` — request the one active order book; returns `202` while its first live snapshot is opening
- `DELETE /api/books/{ticker}` — release the active book when the game dropdown closes
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
- Main spread and total lines are selected from the active strike closest to a 50% midpoint. Up to five nearby strikes are retained for the inline line selector.
- Clicking a game expands its order book in place. Only the selected ticker receives a book subscription; selecting another ticker cancels the old stream, and closing the dropdown releases it. The authenticated account stream remains independent and continuously connected.
- The Yes and No tabs are real views of the same binary book: the No ladder is derived by complementing the synchronized Yes-price book. Clicking any bid or ask copies its exact side and price into the floating order slip. Submission stays disabled because no trading endpoint exists yet.
- Kalshi sequence numbers are subscription-wide, not ticker-wide. A book-stream gap forces that selected ticker to reconnect and remain stale until a new snapshot arrives.
- Sports preferences are stored in SQLite. No saved preference means all sports; saving an empty selection intentionally loads no sports.
- Extra/added games are identified by an exactly six-digit numeric schedule event ID. The Settings tab can exclude them before market matching and subscription.
- Simulated events include selectable moneyline, spread, and total quotes. Six-digit added games use lower simulated available quantities.

## Known limitations

- Kalshi live order authentication and on-demand book streaming have been validated read-only against a production account. Validation mapped 2,240 contracts into 441 selectable game/strike books; a requested book moved from `202` to a synchronized live ladder, switching tickers opened only the replacement stream, and release returned `204`. Position/fill historical REST reconciliation remains the next account-data task.
- Initial league-to-series routing covers the major US leagues plus selected top soccer leagues. Add aliases as new schedule leagues are enabled; unknown leagues intentionally load no Kalshi series.
- The current general Kalshi fee rule is versioned in one module, but market-specific fee exceptions must be added before any production order preview.
- The UI is intentionally read-only and contains no submit-capable order form.
- The order slip currently previews intent and risk controls but cannot submit. This is deliberate production lockout, not a partially connected trading path.
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

## Lightweight checkpoint

- Browser production bundle: about 66 KB JavaScript and 14 KB CSS before gzip; there are no runtime browser dependencies beyond Svelte.
- Production source maps are disabled and old side-panel CSS/dead book code were removed.
- Runtime background work is bounded: one 30-second schedule ticker, one authenticated account stream, and zero or one selected order-book stream.
- The single Windows executable is about 18 MB, primarily because it embeds the pure-Go SQLite implementation and the complete browser app; deployment still requires only that one executable.
