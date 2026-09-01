# First live order: a checklist for the owner

This is the test the whole project has been building toward. You do every step yourself, in the browser. No AI session may place, change, or cancel an order for you, and none should be running against your production keys while you do this.

Read it once end to end before you start. Budget about an hour. Have the Kalshi website open in a second tab so you can compare what PMBattle shows against what Kalshi shows.

## Before you start

- [ ] You are on the latest code: in PowerShell, inside the project folder, run `git pull`, then `cd web`, `npm run build`, `cd ..`, `go build -o pmbattle.exe .`
- [ ] You have a start script outside the project folder (for example `C:\secure\start-live-test.ps1`) with these lines. Fill in your own key ID and key path.

```powershell
$env:PMBATTLE_KALSHI_ENV = "production"
$env:PMBATTLE_KALSHI_KEY_ID = "your-key-id"
$env:PMBATTLE_KALSHI_PRIVATE_KEY_PATH = "C:\secure\kalshi-private-key.pem"
$env:PMBATTLE_SIMULATED = "false"
$env:PMBATTLE_MAX_CASH_RISK = "5"
$env:PMBATTLE_TRADING_ENABLED = "true"
Set-Location $HOME\pmbattle
.\pmbattle.exe
```

- [ ] `PMBATTLE_MAX_CASH_RISK` is set to a small number. With `5`, no single order can put more than five dollars at risk no matter what is typed into the order slip.
- [ ] You know how to stop the server: click the PowerShell window and press Ctrl + C. Stopping the server does not cancel orders already resting on Kalshi; cancel those in PMBattle or on Kalshi's site first.

## Part 1: read-only sanity check

Start the script. Open http://127.0.0.1:8080.

- [ ] The top bar shows **LIVE · CONNECTED**.
- [ ] Settings shows Environment **LIVE**, Account sync **READY**, a mapped-market count in the hundreds, and the badge **LIVE TRADING**.
- [ ] Settings shows **Per-order cap $5.00**.
- [ ] The note under the safety grid says real Kalshi orders are enabled and explains how to lock them again.
- [ ] "Available to trade" matches the available balance on Kalshi's site to the cent.
- [ ] Open the Positions tab in the bottom dock. Every open position on Kalshi's site is listed, with the same quantities.

If any line above is wrong, stop here and describe what you saw to your next AI session. Do not place an order.

## Part 2: one basic order that will not fill

The goal is to see a real order appear on Kalshi, then cancel it, without it ever filling.

- [ ] Pick a game with a liquid moneyline (a big-league game starting in a few hours is ideal). Click the game to open its book. Wait for the book state to say **LIVE**.
- [ ] Click a **BID** row several levels below the top bid. The order slip opens with that price and **Join** selected.
- [ ] In the slip: strategy **Basic**, order behavior **Post only**, cash at risk **2**. Leave the fee-adjusted cap as filled in.
- [ ] Click **Review & place real order**. Read the confirmation dialog. It must name the team and a cash-at-risk of about two dollars. Confirm.
- [ ] The slip shows "Parent order ... created". Open the **Orders** tab in the bottom dock. Your order is listed with status `resting` (or similar) and a cash risk at or under two dollars.
- [ ] On Kalshi's site, open your open orders. The same order is there at the same price and contract count. Write down the contract count shown by Kalshi and by PMBattle. They should match.
- [ ] Open History, then **System audit**. You should see `parent order request` and `parent order acknowledged` entries for this order.
- [ ] Back in the Orders tab, click **Cancel** on the order. Status changes to `canceled`. Cash at risk in the top bar drops back.
- [ ] On Kalshi's site, the order is gone from open orders.

Result to record for the next session: order placed, seen on Kalshi, canceled cleanly, counts matched (yes or no).

## Part 3: one basic order that fills

This spends a small amount of real money on purpose. Skip it if Part 2 had any mismatch.

- [ ] Open the same game. Click the top **ASK** row. The slip opens with **Buy** selected and order behavior **Limit**.
- [ ] Cash at risk **2**. Confirm the dialog.
- [ ] Within a few seconds a **FILL RECEIVED** notice appears in the corner and the Fills tab gains a row.
- [ ] Compare the fill row's **Fee** with the fee Kalshi shows for that trade. Write both numbers down. If Kalshi's fee is higher, the app's fee rounding needs adjusting; that is expected to be at most one cent.
- [ ] The Positions tab now shows the new position. "Cash at risk" in the top bar increased by roughly the fill's cash risk.
- [ ] On Kalshi's site, the position and the fee match.

## Part 4: lock it again

- [ ] Stop the server with Ctrl + C.
- [ ] Edit the start script: set `PMBATTLE_TRADING_ENABLED = "false"`.
- [ ] Start it again. Settings must show **READ-ONLY** and the note "Production order entry is off until enabled on the server."

## What to tell the next AI session

Paste something like this as your first message after the usual "read the handoff" line:

> I ran FIRST-LIVE-ORDER.md on [date]. Part 1: all lines OK. Part 2: order resting on Kalshi, counts matched [yes/no], cancel worked [yes/no]. Part 3: fill received [yes/no], PMBattle fee [x], Kalshi fee [y]. Anything odd: [describe]. Update HANDOFF.md with these results and fix anything that did not match.

## Do not do these yet

- Do not test **Iceberg** or **Follow** live. Follow depends on Kalshi honoring post-only and on how it answers an amend, and both are still unverified. The handoff explains what has to be confirmed first.
- Do not raise `PMBATTLE_MAX_CASH_RISK` until basic orders have matched Kalshi exactly, twice.
- Do not leave `PMBATTLE_TRADING_ENABLED = "true"` running unattended.
