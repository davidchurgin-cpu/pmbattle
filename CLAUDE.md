# PMBattle: instructions for AI coding sessions

Read this file first, then `HANDOFF.md`, then `README.md`. Do not start editing until you have.

## Who you are working for

The owner is not a programmer. They describe what they want in plain language and rely on you to make sound engineering decisions, explain them simply, and leave the project in a state the next session can pick up without them having to remember anything technical. Write for them: short sentences, no jargon without a one-line explanation, and always say what to expect on screen.

## Hard rules

1. **Never place, amend, cancel, or resume an order.** Not in production, not in demo, not "just to test". Only the owner does that, in the browser. Do not write scripts, tests, or tools that send real order mutations to Kalshi. The automated tests use a fake exchange adapter and must keep doing so.
2. **Never enable trading.** Do not set `PMBATTLE_TRADING_ENABLED=true` in any file, script, or environment you control. The owner sets it themselves in their own start script when they are actively testing.
3. **Never commit secrets.** No `.pem` files, no `.env`, no `pmbattle.db`, no key IDs, no passwords. If you see one in the working tree, stop and tell the owner.
4. **Never weaken a safety check** to make a test pass or a feature work. Fee caps, cash-risk caps, the startup trading lock, the request-header and same-origin checks, and the stale-book refusals are load-bearing. The owner has chosen not to have a login; do not add one back unless asked.
5. **Never rewrite git history** on `main` or on a branch you did not create.

## How to work

- Before changing anything, run `go test ./...` and report the result. If it is not green, fix that first or tell the owner.
- One change per request. Finish it completely, including tests and docs, before starting another.
- After every change: `go test ./...`, then `go build .`. If the browser page changed: `cd web && npm run check && npm run build`, and commit the rebuilt `web/dist` with the source. The executable embeds `web/dist`, so an unbuilt page ships stale.
- Commit small and often with a clear one-line message. Push when tests are green.
- **Update `HANDOFF.md` in the same commit** whenever behavior, configuration, or the "what works" list changes. That file is the project's memory across sessions and computers. Keep the "Next milestone" section current so the next session knows exactly where to start.
- Prefer editing existing packages over adding new ones. The layout in `HANDOFF.md` under "Architecture" is the map.

## Project facts you need

- Go server (`main.go`, `internal/`) with an embedded Svelte page (`web/`). SQLite file `pmbattle.db` holds state. Single executable.
- Money is fixed-point: `domain.Money` is ten-thousandths of a dollar. Never use floating point for cost, fees, or risk.
- Trading is off unless the server starts with `PMBATTLE_SIMULATED=false`, a Kalshi environment, credentials, and `PMBATTLE_TRADING_ENABLED=true`. Both the service and the Kalshi adapter check this independently.
- The order path (place, amend, cancel) has been exercised only against the fake adapter in tests. Read-only account and market access has been validated against production. Treat every Kalshi API detail in `internal/kalshi/client.go` as "believed correct, not yet proven live".
- Validation commands: `go test ./...`, `go build .`, and in `web/`: `npm ci && npm run check && npm run build`. GitHub Actions runs the same on every push.

## When the owner asks you to do something risky

Explain the risk in two sentences, propose the safer version, and wait. If they confirm, do exactly what they asked and say so plainly in the commit message and in `HANDOFF.md`.
