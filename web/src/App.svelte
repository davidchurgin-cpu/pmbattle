<script lang="ts">
  import { onMount } from 'svelte'
  import type { Event, Fill, Health, Order, OrderBook, Position, PriceQuote, Snapshot } from './types'

  let snapshot: Snapshot = { events: [], orders: [], positions: [], fills: [], health: { status: 'starting', mode: 'simulated', scheduleUpdated: '', exchangeState: 'disconnected', latencyMs: 0, tradingEnabled: false }, bankroll: 0, atRisk: 0 }
  let query = ''
  let selectedSport = 'ALL'
  let selectedLeague = 'ALL'
  let selectedDate = 'ALL'
  let selectedQuote: PriceQuote | null = null
  let selectedEvent: Event | null = null
  let book: OrderBook | null = null
  let trayOpen = true
  let trayTab: 'orders' | 'positions' | 'fills' | 'history' = 'fills'
  let theme: 'light' | 'dark' = (localStorage.getItem('pmbattle-theme') as 'light' | 'dark') || 'dark'
  let error = ''

  const money = (value: number) => `$${(value / 10000).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
  const qty = (value: number) => (value / 10000).toLocaleString(undefined, { maximumFractionDigits: 2 })
  const ml = (value?: number) => value === undefined ? '—' : value > 0 ? `+${value}` : `${value}`
  const dateKey = (value: string) => new Date(value).toISOString().slice(0, 10)
  const time = (value: string) => new Date(value).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
  const day = (value: string) => new Date(value).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })

  $: sports = ['ALL', ...new Set(snapshot.events.map(event => event.sport.toUpperCase()))]
  $: leagues = ['ALL', ...new Set(snapshot.events.filter(event => selectedSport === 'ALL' || event.sport.toUpperCase() === selectedSport).map(event => event.league.toUpperCase()))]
  $: dates = ['ALL', ...new Set(snapshot.events.map(event => dateKey(event.startTime)))]
  $: filtered = snapshot.events.filter(event => {
    const searchable = `${event.id} ${event.sport} ${event.league} ${event.participants.map(p => `${p.rotation} ${p.name} ${p.abbreviation}`).join(' ')}`.toLowerCase()
    return (!query || searchable.includes(query.toLowerCase())) && (selectedSport === 'ALL' || event.sport.toUpperCase() === selectedSport) && (selectedLeague === 'ALL' || event.league.toUpperCase() === selectedLeague) && (selectedDate === 'ALL' || dateKey(event.startTime) === selectedDate)
  })

  function setTheme(value: 'light' | 'dark') { theme = value; localStorage.setItem('pmbattle-theme', value) }
  async function select(event: Event, quote?: PriceQuote) {
    if (!quote) return
    selectedEvent = event; selectedQuote = quote; book = null
    try { const response = await fetch(`/api/books/${encodeURIComponent(quote.ticker)}`); if (response.ok) book = await response.json() } catch { /* the ticker stream may not have produced a book yet */ }
  }
  function applyStream(message: { type: string; data: unknown }) {
    if (message.type === 'schedule') snapshot = { ...snapshot, events: message.data as Event[] }
    if (message.type === 'health') snapshot = { ...snapshot, health: message.data as Health }
    if (message.type === 'orderbook') { const next = message.data as OrderBook; if (next.ticker === selectedQuote?.ticker) book = next }
    if (message.type === 'book_stale') { const next = message.data as OrderBook; if (next.ticker === selectedQuote?.ticker) book = { ...next, stale: true } }
    if (message.type === 'fill') { const next = message.data as Fill; snapshot = { ...snapshot, fills: [next, ...snapshot.fills].slice(0, 250) } }
    if (message.type === 'order') { const next = message.data as Order; snapshot = { ...snapshot, orders: [next, ...snapshot.orders.filter(order => order.id !== next.id)] } }
    if (message.type === 'position') { const next = message.data as Position; snapshot = { ...snapshot, positions: [next, ...snapshot.positions.filter(position => position.ticker !== next.ticker)] } }
  }
  function connect() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${location.host}/api/ws`)
    socket.onmessage = event => applyStream(JSON.parse(event.data))
    socket.onclose = () => setTimeout(connect, 1500)
  }
  onMount(async () => {
    document.documentElement.dataset.theme = theme
    try { const response = await fetch('/api/snapshot'); if (!response.ok) throw new Error(`Server returned ${response.status}`); snapshot = await response.json(); const first = snapshot.events.find(event => event.markets?.[0]?.away || event.markets?.[0]?.home); if (first) select(first, first.markets?.[0]?.home || first.markets?.[0]?.away); connect() } catch (cause) { error = cause instanceof Error ? cause.message : 'Unable to load PMBattle' }
  })
  $: document.documentElement.dataset.theme = theme
</script>

<svelte:head><title>PMBattle</title><meta name="description" content="Fast sportsbook-style prediction market terminal"></svelte:head>

<div class="app-shell">
  <header class="topbar">
    <strong class="brand">PMBATTLE</strong>
    <label class="search"><span aria-hidden="true">⌕</span><input bind:value={query} aria-label="Search games" placeholder="Search game # or team" /></label>
    <div class="health" class:is-stale={snapshot.health.status !== 'ok'}><i></i><span>{snapshot.health.mode.toUpperCase()} · {snapshot.health.exchangeState.toUpperCase()}</span></div>
    <div class="theme"><button class:active={theme === 'light'} on:click={() => setTheme('light')}>Light</button><button class:active={theme === 'dark'} on:click={() => setTheme('dark')}>Dark</button></div>
  </header>
  <nav class="sports" aria-label="Sport filters">
    {#each sports as sport}<button class:active={selectedSport === sport} on:click={() => { selectedSport = sport; selectedLeague = 'ALL' }}>{sport}</button>{/each}
    <span class="account">Bankroll <b>{money(snapshot.bankroll)}</b> · At risk <b>{money(snapshot.atRisk)}</b></span>
  </nav>
  <div class="filters">
    <select bind:value={selectedDate} aria-label="Date"><option value="ALL">All dates</option>{#each dates.slice(1) as date}<option value={date}>{new Date(`${date}T12:00:00`).toLocaleDateString()}</option>{/each}</select>
    <select bind:value={selectedLeague} aria-label="League"><option value="ALL">All leagues</option>{#each leagues.slice(1) as league}<option>{league}</option>{/each}</select>
    <span>{filtered.length} games</span>
  </div>

  {#if error}<div class="error" role="alert">{error}</div>{/if}
  <div class="workspace">
    <main class="board">
      <div class="board-head"><span>Game</span><span>Team</span><span>Moneyline</span><span>Spread</span><span>Total</span><span>Time</span></div>
      {#each filtered as event (event.id)}
        {@const market = event.markets?.[0]}
        <section class="game" class:selected={selectedEvent?.id === event.id}>
          <div class="rotations">{#each event.participants as participant}<b>{participant.rotation}</b>{/each}</div>
          <div class="teams">{#each event.participants as participant}<div><strong>{participant.name}</strong><small>{participant.abbreviation}</small></div>{/each}</div>
          <div class="market">{#if market}{#each [market.away, market.home] as quote}<button class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click={() => select(event, quote)}><b>{ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Unavailable'}</small></button>{/each}{:else}<span>—</span><span>—</span>{/if}</div>
          <div class="market muted"><span>—</span><span>—</span></div><div class="market muted"><span>—</span><span>—</span></div>
          <div class="start"><b>{day(event.startTime)}</b><span>{time(event.startTime)}</span></div>
        </section>
      {:else}<div class="empty">No matching games</div>{/each}
    </main>

    <aside class="detail">
      <div class="detail-title"><b>Live order book</b><span class:stale={book?.stale}><i></i>{book?.stale ? 'STALE' : 'LIVE'} · {snapshot.health.latencyMs || 18} ms</span></div>
      {#if selectedQuote && selectedEvent}
        <div class="selection"><b>#{selectedEvent.participants.find(p => p.name === selectedQuote?.outcome)?.rotation || ''} {selectedQuote.outcome}</b><span>All-in {ml(selectedQuote.allInMoneyline)} · Raw {ml(selectedQuote.rawMoneyline)}</span></div>
        <div class="book-head"><span>Side</span><span>Size</span><span>Raw</span><span>All-in</span></div>
        {#if book}
          {#each [...book.yes.slice(0, 4)] as level}<div class="book-row"><span>YES</span><span>{qty(level.quantity)}</span><span>{money(level.price)}</span><span>{ml(selectedQuote.allInMoneyline)}</span></div>{/each}
          {#each [...book.no.slice(0, 4)] as level}<div class="book-row"><span>NO</span><span>{qty(level.quantity)}</span><span>{money(level.price)}</span><span>{ml(selectedQuote.allInMoneyline)}</span></div>{/each}
        {:else}
          <div class="book-row"><span>YES</span><span>{qty(selectedQuote.availableQuantity)}</span><span>{money(selectedQuote.rawPrice)}</span><span>{ml(selectedQuote.allInMoneyline)}</span></div>
          <p class="waiting">Waiting for book snapshot…</p>
        {/if}
        <dl><div><dt>Maker fee</dt><dd>{money(selectedQuote.makerFee)}</dd></div><div><dt>Taker fee</dt><dd>{money(selectedQuote.takerFee)}</dd></div><div><dt>Mapping</dt><dd>Verified</dd></div></dl>
      {:else}<div class="empty">Select a market price</div>{/if}
      <div class="readonly">READ-ONLY RELEASE · ORDER ENTRY DISABLED</div>
    </aside>
  </div>

  <section class="tray" class:open={trayOpen}>
    <div class="tray-tabs">
      <button class:active={trayTab === 'positions'} on:click={() => { trayTab = 'positions'; trayOpen = true }}>Positions ({snapshot.positions.length})</button>
      <button class:active={trayTab === 'orders'} on:click={() => { trayTab = 'orders'; trayOpen = true }}>Orders ({snapshot.orders.length})</button>
      <button class:active={trayTab === 'fills'} on:click={() => { trayTab = 'fills'; trayOpen = true }}>Live fills</button>
      <button class:active={trayTab === 'history'} on:click={() => { trayTab = 'history'; trayOpen = true }}>History</button>
      <button class="tray-toggle" on:click={() => trayOpen = !trayOpen}>{trayOpen ? 'Collapse ↓' : 'Open ↑'}</button>
    </div>
    {#if trayOpen}<div class="tray-body">
      {#if trayTab === 'fills'}
        <div class="table-head"><span>Time / market</span><span>Exchange</span><span>Quantity</span><span>Raw</span><span>All-in</span><span>Fee</span><span>Cash risk</span></div>
        {#each snapshot.fills as fill}<div class="table-row"><span><b>{new Date(fill.createdAt).toLocaleTimeString()} · #{fill.rotation}</b><small>{fill.team} {fill.market}</small></span><span>{fill.exchange}</span><span>{qty(fill.quantity)}</span><span>{money(fill.rawPrice)}</span><span>{ml(fill.allInMoneyline)}</span><span>{money(fill.fee)}</span><span>{money(fill.cashRisk)}</span></div>{:else}<div class="empty">No fills yet</div>{/each}
      {:else if trayTab === 'orders'}
        {#each snapshot.orders as order}<div class="table-row compact"><span><b>#{order.rotation} {order.market}</b><small>{order.ticker}</small></span><span>{order.exchange}</span><span>{qty(order.quantity)}</span><span>{money(order.limitPrice)}</span><span>{order.status}</span><span>—</span><span>{money(order.cashRisk)}</span></div>{:else}<div class="empty">No pending orders</div>{/each}
      {:else if trayTab === 'positions'}
        {#each snapshot.positions as position}<div class="table-row compact"><span><b>#{position.rotation} {position.market}</b><small>{position.ticker}</small></span><span>{position.exchange}</span><span>{qty(position.quantity)}</span><span>{money(position.averagePrice)}</span><span>{money(position.currentPrice)}</span><span>—</span><span>{money(position.unrealizedPnl)}</span></div>{:else}<div class="empty">No positions</div>{/each}
      {:else}<div class="empty">Historical audit records will appear here.</div>{/if}
    </div>{/if}
  </section>
</div>

