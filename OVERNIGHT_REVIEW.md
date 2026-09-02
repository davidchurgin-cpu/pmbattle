# PMBattle overnight review

Date: September 1–2, 2026

## Guardrails

- Keep every change local and uncommitted for morning review.
- Do not push to GitHub.
- Do not place, edit, or cancel real orders.
- Work in small batches and stop after tests at each checkpoint.
- Preserve the running production server and its existing account state.

## Morning review order

1. Do not judge the current `127.0.0.1:8080` page as the overnight result: the production executable and embedded `web/dist` were intentionally left untouched.
2. Locate/install the Go toolchain, run `gofmt` on the modified Go files, then run `go test ./...` and `go build .`. Checkpoint 17 is not approved until these pass.
3. Run `npm run check --prefix web` and `npm run build --prefix web` after reviewing the source changes. The repeated source checks passed overnight; no distribution build was made.
4. Review checkpoint 17 first because it changes server-side amendment bankroll accounting. Then review the smaller browser batches in chronological order below.
5. Test the candidate build on a separate port or with trading disabled. Do not replace the currently running production process until the diff is accepted.
6. Commit and push only the accepted changes; the overnight session made no commits or pushes.

## Morning state

### Final review — September 2, 2026

- Reviewed the complete pending backend, frontend, generated-bundle, and documentation diff.
- Corrected one display-only quantity fallback so order-row fee odds use Kalshi's 0.01-contract minimum rather than one whole contract.
- `npm run check`: 0 errors and 0 warnings.
- `npm run build`: successful; 106.30 KB JavaScript and 24.79 KB CSS before gzip.
- `go test ./...`: all packages passed under Go 1.26.5.
- Embedded Windows executable build: successful.
- `git diff --check`: no whitespace errors (only expected Windows line-ending notices).
- Browser smoke test used a separate simulated, read-only server on port 18080. Schedule/filtering, expanded live book, clickable prices, fee-inclusive sizing, Settings refresh, and browser console were verified. No exchange mutation occurred and the production server on port 8080 was not changed.

- Modified: `internal/app/app.go`, `internal/app/reconciliation_test.go`, `internal/server/server.go`, `web/src/App.svelte`, `web/src/orderslip.css`, `web/src/styles.css`, and `web/src/types.ts`.
- New local review file: `OVERNIGHT_REVIEW.md`.
- Frontend source validation passed after every browser checkpoint, most recently with 0 errors and 0 warnings.
- `git diff --check` found no whitespace errors; Windows line-ending notices are expected.
- No real order was placed, edited, or canceled. No server was restarted. No build was deployed.

## Checkpoints

### 19:30 — Overnight session initialized

- Confirmed the existing PMBattle build goal remains active.
- Scheduled recurring review checkpoints in this thread.
- Selected order-row clarity as the first non-mutating batch: remaining quantity, raw-to-fee-included American odds, and order time.
- No code committed or pushed. No exchange mutation performed.

### 19:35 — Checkpoint 1: order-row clarity

- Added remaining-contract display to working order rows.
- Added raw → fee-included American odds beside the exact cent limit.
- Added relative order age beside the game and bet name.
- Frontend validation: 0 errors and 0 warnings.
- Left the production server unchanged; this batch exists only in local source for morning review.
- No code committed or pushed. No exchange mutation performed.

### 20:00 — Checkpoint 2: manual account synchronization

- Added a read-only `Refresh account` control to Trading status in Settings.
- Added serialized account reconciliation so manual and periodic refreshes cannot interleave their snapshots.
- The control returns refreshed orders, positions, fills, balances, exposure, and synchronization time in one response.
- Added backend regression coverage for the reconciled snapshot.
- Full Go test suite passes.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 20:30 — Checkpoint 3: keyboard speed controls

- Added `/` to jump directly to instant game/team search.
- Added Up/Down arrows to move through the visible filtered schedule without reaching for the mouse.
- Added Escape to close the active edit, order slip, account tray, or expanded order book in that priority order.
- Kept shortcuts inactive inside inputs, selects, and buttons so order editing cannot be disrupted.
- Added a compact shortcut reminder beside the game count.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 21:00 — Checkpoint 4: browser live-stream resilience

- Replaced the page's unbounded fixed-delay reconnect loop with one managed WebSocket connection.
- Added capped exponential reconnect timing (one to ten seconds), preventing duplicate reconnect timers and abandoned sockets.
- Added clean socket/timer shutdown when the page is closed or replaced.
- One malformed browser event is now isolated instead of breaking all later UI updates.
- Added a compact `UI LIVE`, `UI CONNECTING`, or `UI RECONNECTING` state to distinguish the browser link from Kalshi's exchange connection.
- Frontend validation: 0 errors and 0 warnings.
- The Go executable is not installed on this overnight runner's command path, so the Go suite could not be rerun at this checkpoint; it passed after checkpoint 2 and checkpoint 4 changes only frontend code.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 21:30 — Checkpoint 5: lightweight fill-history rendering

- Kept all recovered fills available in the browser while limiting the initial tray render to the newest 40 rows.
- Added an explicit `Show 40 earlier fills` control with a visible shown/total count.
- Reopening Fills returns immediately to the newest activity instead of retaining a large historical render.
- New live fills remain at the top and still drive the existing notification count.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 22:00 — Checkpoint 6: lightweight sportsbook-board rendering

- Kept the complete normalized schedule in memory but limited the initial board DOM to 80 games.
- Added an explicit 80-game continuation control and visible shown/total counts for broad schedule views.
- Search, sport, date, and league changes reset immediately to the newest first batch.
- Keyboard navigation now follows only rows that actually exist on screen.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 22:30 — Checkpoint 7: stale-book interaction guard

- Disabled bid and ask rows whenever the browser stream or selected order book is not synchronized.
- Prevented stale ladder prices from populating the order slip.
- Disabled order review on an already-open slip until a fresh book snapshot arrives and added a plain-language warning.
- Added selected-book update age and sequence to the compact footer for troubleshooting.
- Preserved the server's existing stale-book refusal as the final authority.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 23:00 — Checkpoint 8: fee-inclusive cash-risk preview

- Replaced the slip's raw-price division estimate with the same conservative binary sizing approach used by the server.
- Contract estimates now round down to Kalshi's 0.01-contract increment only after including the taker fee.
- The preview separately shows estimated contracts, fee, and actual all-in reserved cash.
- Disabled review when the selected risk cannot fund even the minimum valid quantity.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 00:15 — Checkpoint 9: account-link navigation across board batches

- Fixed order and position links so they reveal and scroll to the correct market even when its game is beyond the first 80 rendered rows.
- Preserved support for linked historical games by including the requested event while determining its board position.
- Arrow-down navigation now loads the next 80-game batch automatically at the visible boundary.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 00:45 — Checkpoint 10: fee-inclusive order-edit preview

- Added a live all-in risk estimate while editing a resting order's remaining quantity or limit.
- The estimate uses the same conservative taker-fee calculation shown throughout the terminal.
- Save is disabled when the proposed edit exceeds the configured per-order cap, while the server remains the final enforcement layer.
- Real-order confirmation now includes the estimated all-in risk, making the pending change explicit before submission.
- Frontend validation: 0 errors and 0 warnings.
- Risk note: this is an estimate at the entered limit and does not replace Kalshi's acknowledgement or later reconciliation.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 01:15 — Checkpoint 11: reliable all-in price-cap defaults

- Changed the order slip's automatic worst-price limit to use the user's fee-inclusive sized preview, rather than the full displayed ladder level's quantity.
- The automatic limit now recalculates when cash risk changes, accounting for quantity-sensitive fee rounding.
- Once the user manually edits the limit, it remains user-controlled and is no longer overwritten.
- Added American-odds validation and a clear warning when the current fee-included quote is already worse than the chosen limit.
- Disabled review for invalid or already-breached limits while preserving server-side enforcement.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 01:45 — Checkpoint 12: maker/taker order-slip estimates

- Split the browser's fixed-point fee preview into reusable maker and taker calculations using Kalshi's configured general rates.
- The order slip now shows both fee-adjusted American-odds estimates and both fee amounts for the user's exact sized quantity.
- Cash reservation and the automatic hard price cap remain based on the conservative taker result, including for orders intended to rest.
- Relabeled the headline conversion to make its conservative taker assumption explicit.
- Frontend validation: 0 errors and 0 warnings.
- Risk note: market-specific Kalshi fee exceptions remain a documented follow-up; this preview matches the application's current versioned general fee rule.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 02:15 — Checkpoint 13: automatic initial-load recovery

- Added automatic snapshot retry when the browser opens before the server is ready or the first request fails transiently.
- Retry delay backs off from one to ten seconds and resets after a successful load.
- The page clearly reports that it is retrying, then clears the error and starts the live browser stream without requiring a manual refresh.
- Snapshot and WebSocket retry timers are independently managed and both clean up when the page closes.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 02:45 — Checkpoint 14: valid iceberg configuration

- Prevented iceberg submission unless the visible slice is positive and strictly smaller than the fee-sized parent quantity.
- Added an immediate explanation showing the current estimated total when the slice is invalid.
- Restricted immediate-or-cancel behavior to basic orders; selecting Iceberg automatically returns an inherited IOC choice to Limit.
- Follow remains fixed to post-only as before.
- Frontend validation: 0 errors and 0 warnings.
- Risk note: this is a browser guard; server-side strategy validation should also reject unsupported combinations in a later backend checkpoint when the Go toolchain is available.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 03:25 — Checkpoint 15: fills and settlements link back to markets

- Made every mapped fill row keyboard- and mouse-clickable, using the same fast market-opening path as Orders and Positions.
- Made mapped settlement-history rows clickable as well, including support for historical games outside today's default board.
- Added the existing backend `eventId` and `side` fields to the browser Fill type so navigation can use the strongest available mapping evidence before ticker fallback.
- Unmapped or no-longer-discoverable tickers remain harmless: clicking them performs no mutation and leaves the current view unchanged.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 03:55 — Checkpoint 16: scoped real-order cancellation confirmation

- Closed a confirmation gap: strategy-, game-, and exchange-scoped cancellation now require explicit confirmation in live mode, matching individual and all-order cancellation.
- Added a live matched-parent count for the selected scope and placed that count directly on the action button.
- Disabled the scoped action when its exact scope has no matching active managed parents, instead of enabling it based on unrelated parents.
- The all-orders scope continues to count and cover managed plus reconciled/external active Kalshi orders.
- Frontend validation: 0 errors and 0 warnings.
- Risk note: counts are a browser preview; the server recomputes targets at execution time and reports partial failures.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 04:30 — Checkpoint 17: shared-bankroll guard for order amendments

- Closed a server-side risk gap: increasing or repricing a resting basic/reconciled order now checks shared available cash before calling Kalshi.
- Replacement capacity correctly includes the current order's released reservation, while still subtracting hidden future commitments from managed strategies.
- A successful amend immediately adjusts local available cash by the reservation delta; later account reconciliation remains authoritative.
- Added a regression test proving an over-bankroll amend never reaches the exchange and a permitted amend updates available cash correctly.
- Added matching instant browser feedback and disabled Save when the proposed all-in amount exceeds current amend capacity.
- Frontend validation: 0 errors and 0 warnings. `git diff --check` found no whitespace errors.
- Test limitation: the Go regression could not be executed because this overnight runner has no Go executable; it must be the first backend test run during morning review.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 05:00 — Checkpoint 18: safe keyboard order editing

- Fixed nested keyboard handling so Enter/Space on Edit, Save, Close, Resume, or Cancel controls cannot also trigger the surrounding row's market navigation.
- Added Enter-to-save and Escape-to-close directly inside both inline order-edit fields.
- Input keystrokes stop at the edit control, preventing accidental navigation while changing quantity or limit.
- Existing live confirmations and risk guards still run when Enter initiates a save.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 05:30 — Checkpoint 19: safe keyboard market selection

- Fixed the same nested-keyboard event issue on sportsbook game rows.
- Enter/Space now expands a game only when the game row itself is focused.
- Activating a moneyline, spread, or total price button by keyboard no longer also toggles the entire game row.
- Space activation prevents page scrolling while preserving normal button behavior.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 06:05 — Checkpoint 20: uncommitted backend integrity audit

- Reviewed the full pending diff rather than adding another feature on top of an unverified backend batch.
- Found that the checkpoint 17 shared-cash declaration had been inserted into the adjacent cancel path while its amend use was unresolved; corrected the placement before any build or deployment.
- Reconfirmed that parent creation retains its original shared-cash calculation, cancellation has no unused declaration, and amendment owns the new replacement-capacity calculation.
- `git diff --check` reports no whitespace errors; only expected Windows line-ending notices remain.
- Test limitation remains: Go is not installed on this runner, so checkpoint 17 must not be accepted or deployed until its regression and the full Go suite pass in the morning.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.

### 06:35 — Checkpoint 21: no stale window after browser reconnect

- Marked the selected in-browser order book stale immediately when the browser WebSocket closes.
- Reopening the socket no longer makes cached levels actionable by itself; only a subsequent fresh order-book event clears the stale state.
- This closes the brief reconnect window where the connection indicator could return to live before the selected ladder had been refreshed.
- The order slip and ladder guards from checkpoint 7 automatically remain disabled throughout that window.
- Frontend validation: 0 errors and 0 warnings.
- Production executable was not rebuilt or restarted; changes remain local for morning review.
- No code committed or pushed. No exchange mutation performed.
