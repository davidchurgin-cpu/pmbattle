<script lang="ts">
  import { onMount } from 'svelte'
  import type { BookLevel, Event, Fill, Health, MarketOption, MarketView, Order, OrderBook, Position, PriceQuote, Settings, Snapshot } from './types'

  let snapshot: Snapshot = { events: [], orders: [], positions: [], fills: [], health: { status: 'starting', mode: 'simulated', scheduleUpdated: '', exchangeState: 'disconnected', latencyMs: 0, tradingEnabled: false }, bankroll: 0, atRisk: 0, settings: { preferences: { enabledSports: null, excludeAddedGames: false }, availableSports: [] } }
  let view: 'schedule' | 'settings' = 'schedule'
  let draftSports: string[] = []
  let draftExcludeAddedGames = false
  let settingsStatus = ''
  let query = ''
  let selectedSport = 'ALL'
  let selectedLeague = 'ALL'
  let selectedDate = 'ALL'
  let selectedQuote: PriceQuote | null = null
  let selectedEvent: Event | null = null
  let selectedMarket: MarketView | null = null
  let expandedEventID = ''
  let bookSide: 'yes' | 'no' = 'yes'
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
  const spread = (line: string | undefined, away: boolean) => {
    const value = Number(line || 0) * (away ? -1 : 1)
    return value > 0 ? `+${value}` : `${value}`
  }
  const ceilDiv = (value: bigint, divisor: bigint) => (value + divisor - 1n) / divisor
  const takerQuote = (level: BookLevel) => {
    const d = 10000n, price = BigInt(level.price), quantity = BigInt(level.quantity)
    const fee = ceilDiv(700n * quantity * price * (d - price), d * d * d)
    const cost = ceilDiv(price * quantity, d) + fee
    const effective = Number(ceilDiv(cost * d, quantity))
    const moneyline = effective === 5000 ? 100 : effective < 5000 ? Math.round(100 * (10000 - effective) / effective) : -Math.round(100 * effective / (10000 - effective))
    return { fee: Number(fee), cost: Number(cost), moneyline }
  }
  const levelPrice = (level: BookLevel) => `${ml(Math.round(level.price < 5000 ? 100 * (10000 - level.price) / level.price : -100 * level.price / (10000 - level.price)))} → ${ml(takerQuote(level).moneyline)}`
  const marketLabel = (market: MarketView | null) => market?.type === 'spread' ? 'Spread' : market?.type === 'total' ? 'Total' : 'Moneyline'

  $: sports = ['ALL', ...new Set(snapshot.events.map(event => event.sport.toUpperCase()))]
  $: leagues = ['ALL', ...new Set(snapshot.events.filter(event => selectedSport === 'ALL' || event.sport.toUpperCase() === selectedSport).map(event => event.league.toUpperCase()))]
  $: dates = ['ALL', ...new Set(snapshot.events.map(event => dateKey(event.startTime)))]
  $: filtered = snapshot.events.filter(event => {
    const searchable = `${event.id} ${event.sport} ${event.league} ${event.participants.map(p => `${p.rotation} ${p.name} ${p.abbreviation}`).join(' ')}`.toLowerCase()
    return (!query || searchable.includes(query.toLowerCase())) && (selectedSport === 'ALL' || event.sport.toUpperCase() === selectedSport) && (selectedLeague === 'ALL' || event.league.toUpperCase() === selectedLeague) && (selectedDate === 'ALL' || dateKey(event.startTime) === selectedDate)
  })

  function setTheme(value: 'light' | 'dark') { theme = value; localStorage.setItem('pmbattle-theme', value) }
  function editSport(sport: string, enabled: boolean) { draftSports = enabled ? [...new Set([...draftSports, sport])] : draftSports.filter(item => item !== sport); settingsStatus = '' }
  async function savePreferences() {
    settingsStatus = 'Saving…'
    try {
      const response = await fetch('/api/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabledSports: draftSports, excludeAddedGames: draftExcludeAddedGames }) })
      if (!response.ok) throw new Error('Unable to save sports preferences')
      snapshot = await response.json(); draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name); selectedSport = 'ALL'; selectedLeague = 'ALL'; selectedDate = 'ALL'; settingsStatus = 'Saved'
    } catch (cause) { settingsStatus = cause instanceof Error ? cause.message : 'Unable to save settings' }
  }
  async function select(event: Event, quote?: PriceQuote, market?: MarketView) {
    if (!quote) return
    selectedEvent = event; selectedQuote = quote; selectedMarket = market || event.markets?.find(value => [value.away, value.home, value.over, value.under].some(valueQuote => valueQuote?.ticker === quote.ticker)) || null; expandedEventID = event.id; book = null
    try { const response = await fetch(`/api/books/${encodeURIComponent(quote.ticker)}`); if (response.ok) book = await response.json() } catch { /* the live snapshot will arrive over the browser stream */ }
  }
  function toggleGame(event: Event) {
    if (expandedEventID === event.id) { if (selectedQuote) fetch(`/api/books/${encodeURIComponent(selectedQuote.ticker)}`, { method: 'DELETE' }).catch(() => {}); expandedEventID = ''; selectedEvent = null; selectedQuote = null; selectedMarket = null; book = null; return }
    const market = event.markets?.find(value => value.home || value.away || value.over || value.under)
    select(event, market?.home || market?.away || market?.over || market?.under, market)
  }
  function selectOption(option: MarketOption) {
    if (!selectedEvent || !selectedMarket || !selectedQuote) return
    let quote: PriceQuote | undefined
    if (selectedMarket.type === 'spread') quote = selectedQuote.outcome === selectedEvent.participants[0]?.name ? option.away : option.home
    if (selectedMarket.type === 'total') quote = selectedQuote.outcome === 'Over' ? option.over : option.under
    const market: MarketView = { ...selectedMarket, line: option.line, away: option.away, home: option.home, over: option.over, under: option.under }
    select(selectedEvent, quote, market)
  }
  function applyStream(message: { type: string; data: unknown }) {
    if (message.type === 'schedule') snapshot = { ...snapshot, events: message.data as Event[] }
    if (message.type === 'health') snapshot = { ...snapshot, health: message.data as Health }
    if (message.type === 'settings') { snapshot = { ...snapshot, settings: message.data as Settings }; draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name); draftExcludeAddedGames = snapshot.settings.preferences.excludeAddedGames }
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
    try { const response = await fetch('/api/snapshot'); if (!response.ok) throw new Error(`Server returned ${response.status}`); snapshot = await response.json(); draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name); draftExcludeAddedGames = snapshot.settings.preferences.excludeAddedGames; connect() } catch (cause) { error = cause instanceof Error ? cause.message : 'Unable to load PMBattle' }
  })
  $: document.documentElement.dataset.theme = theme
</script>

<svelte:head><title>PMBattle</title><meta name="description" content="Fast sportsbook-style prediction market terminal"></svelte:head>

<div class="app-shell">
  <header class="topbar">
    <strong class="brand">PMBATTLE</strong>
    <nav class="primary-nav" aria-label="Application"><button class:active={view === 'schedule'} on:click={() => view = 'schedule'}>Schedule</button><button class:active={view === 'settings'} on:click={() => view = 'settings'}>Settings</button></nav>
    {#if view === 'schedule'}<label class="search"><span aria-hidden="true">⌕</span><input bind:value={query} aria-label="Search games" placeholder="Search game # or team" /></label>{/if}
    <div class="health" class:is-stale={snapshot.health.status !== 'ok'}><i></i><span>{snapshot.health.mode.toUpperCase()} · {snapshot.health.exchangeState.toUpperCase()}</span></div>
    <div class="theme"><button class:active={theme === 'light'} on:click={() => setTheme('light')}>Light</button><button class:active={theme === 'dark'} on:click={() => setTheme('dark')}>Dark</button></div>
  </header>
  {#if view === 'schedule'}
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
        {@const moneyline = event.markets?.find(market => market.type === 'moneyline')}
        {@const spreadMarket = event.markets?.find(market => market.type === 'spread')}
        {@const total = event.markets?.find(market => market.type === 'total')}
        <div class="game-wrap" class:expanded={expandedEventID === event.id}>
        <section class="game" class:selected={selectedEvent?.id === event.id} role="button" tabindex="0" on:click={() => toggleGame(event)} on:keydown={(key) => { if (key.key === 'Enter' || key.key === ' ') toggleGame(event) }}>
          <div class="rotations">{#each event.participants as participant}<b>{participant.rotation}</b>{/each}</div>
          <div class="teams">{#each event.participants as participant}<div><strong>{participant.name}</strong><small>{participant.abbreviation}</small></div>{/each}</div>
          <div class="market">{#if moneyline}{#each [moneyline.away, moneyline.home] as quote}<button class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, moneyline)}><b>{ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Unavailable'}</small></button>{/each}{:else}<span>—</span><span>—</span>{/if}</div>
          <div class="market">{#if spreadMarket}{#each [spreadMarket.away, spreadMarket.home] as quote, index}<button class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, spreadMarket)}><b>{spread(spreadMarket.line, index === 0)} · {ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Unavailable'}</small></button>{/each}{:else}<span>—</span><span>—</span>{/if}</div>
          <div class="market">{#if total}{#each [total.over, total.under] as quote, index}<button class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, total)}><b>{index === 0 ? 'O' : 'U'} {total.line} · {ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Unavailable'}</small></button>{/each}{:else}<span>—</span><span>—</span>{/if}</div>
          <div class="start"><b>{day(event.startTime)}</b><span>{time(event.startTime)}</span><small>{expandedEventID === event.id ? 'Close ▲' : 'Book ▼'}</small></div>
        </section>
        {#if expandedEventID === event.id && selectedEvent?.id === event.id && selectedQuote && selectedMarket}
          <section class="inline-book" aria-label="Live order book">
            <header class="book-toolbar">
              <div class="book-market-title"><small>{marketLabel(selectedMarket)}</small><h2>{selectedQuote.outcome}{selectedMarket.line ? ` ${selectedMarket.line}` : ''}</h2></div>
              {#if selectedMarket.options?.length}
                <div class="strikes" aria-label="Market lines">
                  {#each selectedMarket.options as option}<button class:active={option.line === selectedMarket.line} on:click={() => selectOption(option)}>{option.line}</button>{/each}
                </div>
              {/if}
              <div class="top-quote"><b>{ml(selectedQuote.allInMoneyline)}</b><small>fee included</small></div>
              <div class="book-state" class:stale={!book || book.stale}><i></i>{!book ? 'CONNECTING' : book.stale ? 'STALE' : 'LIVE'}</div>
            </header>
            <nav class="trade-tabs"><button class:active={bookSide === 'yes'} on:click={() => bookSide = 'yes'}>Trade Yes</button><button class:active={bookSide === 'no'} on:click={() => bookSide = 'no'}>Trade No</button><span>READ-ONLY</span></nav>
            <div class="ladder-head"><span></span><span>Price <small>raw → fee included</small></span><span>Contracts</span><span>Total</span></div>
            {#if book && (book.no.length || book.yes.length)}
              <div class="ladder asks">
                {#each [...book.no.slice(0, 6)].reverse() as level}
                  <div class="ladder-row" style={`--depth:${Math.min(100, Number(level.quantity) / Math.max(1, ...book.no.map(value => Number(value.quantity))) * 100)}%`}><b>ASK</b><span>{levelPrice(level)}</span><span>{qty(level.quantity)}</span><span>{money(takerQuote(level).cost)}</span></div>
                {/each}
              </div>
              <div class="book-center"><b>Trade {bookSide === 'yes' ? 'Yes' : 'No'}</b><span>{selectedQuote.outcome}</span></div>
              <div class="ladder bids">
                {#each book.yes.slice(0, 6) as level}
                  <div class="ladder-row" style={`--depth:${Math.min(100, Number(level.quantity) / Math.max(1, ...book.yes.map(value => Number(value.quantity))) * 100)}%`}><b>BID</b><span>{levelPrice(level)}</span><span>{qty(level.quantity)}</span><span>{money(takerQuote(level).cost)}</span></div>
                {/each}
              </div>
            {:else}
              <div class="book-wait"><b>Opening live order book…</b><span>Only this selected market is being loaded.</span></div>
            {/if}
            <footer class="book-footer"><span>Kalshi · {selectedQuote.ticker}</span><span>Maker estimate {money(selectedQuote.makerFee)} · Taker estimate {money(selectedQuote.takerFee)}</span></footer>
          </section>
        {/if}
        </div>
      {:else}<div class="empty">No matching games</div>{/each}
    </main>
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
  {:else}
    <main class="settings-page">
      <header><h1>Settings</h1><p>Choose the sports PMBattle should load and subscribe to. Your choices are saved on this server.</p></header>
      <section class="settings-section">
        <div class="settings-heading"><div><h2>Sports</h2><p>Unchecked sports are removed from the schedule and Kalshi subscriptions.</p></div><div class="settings-actions"><button on:click={() => draftSports = snapshot.settings.availableSports.map(option => option.name)}>Select all</button><button on:click={() => draftSports = []}>Clear</button></div></div>
        <div class="sport-options">
          {#each snapshot.settings.availableSports as option}
            <label><input type="checkbox" checked={draftSports.includes(option.name)} on:change={(event) => editSport(option.name, event.currentTarget.checked)} /><span><b>{option.name}</b><small>{option.eventCount.toLocaleString()} events · {option.addedGameCount.toLocaleString()} added</small></span></label>
          {:else}<div class="empty">Sports will appear after the schedule loads.</div>{/each}
        </div>
        <label class="preference-row"><input type="checkbox" bind:checked={draftExcludeAddedGames} /><span><b>Hide extra / added games</b><small>Exclude games with six-digit event IDs. These markets generally have lower limits.</small></span></label>
        <div class="settings-footer"><span aria-live="polite">{settingsStatus}</span><button class="save-settings" on:click={savePreferences}>Save preferences</button></div>
      </section>
    </main>
  {/if}
</div>
