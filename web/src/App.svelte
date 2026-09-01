<script lang="ts">
  import { onMount } from 'svelte'
  import type { AccountSnapshot, BookLevel, Event, Fill, Health, MarketOption, MarketView, Order, OrderBook, ParentOrder, Position, PriceQuote, Settings, Snapshot } from './types'

  let snapshot: Snapshot = { events: [], parentOrders: [], orders: [], positions: [], fills: [], health: { status: 'starting', mode: 'simulated', scheduleUpdated: '', exchangeState: 'disconnected', latencyMs: 0, tradingEnabled: false }, bankroll: 0, atRisk: 0, settings: { preferences: { enabledSports: null, excludeAddedGames: false }, availableSports: [] } }
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
  let slipOpen = false
  let slipIntent: 'cross' | 'join' = 'cross'
  let slipPrice = 0
  let slipRisk = '100'
  let slipCap = ''
  let slipStrategy: 'basic' | 'iceberg' | 'follow' = 'basic'
  let slipPolicy: 'limit' | 'post_only' | 'ioc' = 'limit'
  let slipSlice = '25'
  let slipStatus = ''
  let cancelingParentID = ''
  let monitorOpen = false
  let unreadFills = 0
  let fillNotices: { key: string; fill: Fill }[] = []
  const seenFillIDs = new Set<string>()
  let book: OrderBook | null = null
  let trayOpen = true
  let trayTab: 'orders' | 'positions' | 'fills' | 'history' = 'fills'
  let theme: 'light' | 'dark' = (localStorage.getItem('pmbattle-theme') as 'light' | 'dark') || 'dark'
  let error = ''

  const money = (value: number) => `$${(value / 10000).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
  const qty = (value: number) => (value / 10000).toLocaleString(undefined, { maximumFractionDigits: 2 })
  const ml = (value?: number) => value === undefined ? '—' : value > 0 ? `+${value}` : `${value}`
  const rawML = (price: number) => Math.round(price < 5000 ? 100 * (10000 - price) / price : -100 * price / (10000 - price))
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
  const levelPrice = (level: BookLevel) => `${ml(rawML(level.price))} → ${ml(takerQuote(level).moneyline)}`
  const marketLabel = (market: MarketView | null) => market?.type === 'spread' ? 'Spread' : market?.type === 'total' ? 'Total' : 'Moneyline'
  type SelectionRole = 'away' | 'home' | 'over' | 'under'
  function quoteRole(event: Event | null, market: MarketView | null, quote: PriceQuote | null): SelectionRole {
    if (market?.type === 'total') return quote?.outcome.toLowerCase() === 'under' ? 'under' : 'over'
    return event?.participants[1]?.name === quote?.outcome ? 'home' : 'away'
  }
  const oppositeRole = (role: SelectionRole): SelectionRole => role === 'away' ? 'home' : role === 'home' ? 'away' : role === 'over' ? 'under' : 'over'
  function pairedQuote(market: MarketView | null, quote: PriceQuote | null, side: 'yes' | 'no') {
    return [market?.away, market?.home, market?.over, market?.under].find(candidate => candidate?.ticker === quote?.ticker && (candidate?.side || 'yes') === side) || null
  }
  const invertLevel = (level: BookLevel): BookLevel => ({ price: 10000 - level.price, quantity: level.quantity })
  $: displayAsks = !book ? [] : bookSide === 'yes' ? book.no.slice(0, 6) : book.yes.slice(0, 6).map(invertLevel)
  $: displayBids = !book ? [] : bookSide === 'yes' ? book.yes.slice(0, 6) : book.no.slice(0, 6).map(invertLevel)

  $: baseRole = quoteRole(selectedEvent, selectedMarket, selectedQuote)
  $: activeQuote = pairedQuote(selectedMarket, selectedQuote, bookSide)
  $: activeRole = activeQuote ? quoteRole(selectedEvent, selectedMarket, activeQuote) : selectedQuote && bookSide !== (selectedQuote.side || 'yes') ? oppositeRole(baseRole) : baseRole
  $: activeOutcome = activeQuote?.outcome || (selectedQuote && bookSide !== (selectedQuote.side || 'yes') ? `Not ${selectedQuote.outcome}` : selectedQuote?.outcome || '')
  $: activeMoneyline = displayAsks[0] ? takerQuote(displayAsks[0]).moneyline : activeQuote?.allInMoneyline || selectedQuote?.allInMoneyline
  $: slipQuantity = slipPrice > 0 ? Math.max(10000, Math.floor((Number(slipRisk) || 0) * 100000000 / slipPrice)) : 10000
  $: slipQuote = slipPrice > 0 ? takerQuote({ price: slipPrice, quantity: slipQuantity }) : null
  $: workingOrders = snapshot.orders.filter(order => !['canceled', 'cancelled', 'executed', 'filled', 'closed', 'rejected'].includes((order.status || '').toLowerCase()))

  function normalizeSnapshot(value: Snapshot): Snapshot {
    return { ...value, events: value.events || [], parentOrders: value.parentOrders || [], orders: value.orders || [], positions: value.positions || [], fills: value.fills || [], settings: { ...value.settings, availableSports: value.settings?.availableSports || [] } }
  }
  function normalizeBook(value: OrderBook): OrderBook { return { ...value, yes: value.yes || [], no: value.no || [] } }
  function fillName(fill: Fill) { return fill.team || fill.market || fill.ticker }
  function dismissNotice(key: string) { fillNotices = fillNotices.filter(notice => notice.key !== key) }
  function notifyFill(fill: Fill) {
    const key = fill.id || `${fill.ticker}-${Date.now()}`
    fillNotices = [{ key, fill }, ...fillNotices.filter(notice => notice.key !== key)].slice(0, 3)
    unreadFills += 1
    setTimeout(() => dismissNotice(key), 12000)
  }
  function toggleMonitor() { monitorOpen = !monitorOpen; if (monitorOpen) unreadFills = 0 }
  function showActivity(tab: 'orders' | 'fills') { trayTab = tab; trayOpen = true; unreadFills = 0 }
  function viewFill(key: string) { dismissNotice(key); monitorOpen = true; unreadFills = 0 }

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
      snapshot = normalizeSnapshot(await response.json()); draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name); selectedSport = 'ALL'; selectedLeague = 'ALL'; selectedDate = 'ALL'; settingsStatus = 'Saved'
    } catch (cause) { settingsStatus = cause instanceof Error ? cause.message : 'Unable to save settings' }
  }
  async function select(event: Event, quote?: PriceQuote, market?: MarketView) {
    if (!quote) return
    selectedEvent = event; selectedQuote = quote; selectedMarket = market || event.markets?.find(value => [value.away, value.home, value.over, value.under].some(valueQuote => valueQuote?.ticker === quote.ticker)) || null; bookSide = quote.side || 'yes'; expandedEventID = event.id; slipOpen = false; book = null
    try { const response = await fetch(`/api/books/${encodeURIComponent(quote.ticker)}`); if (response.ok) book = normalizeBook(await response.json()) } catch { /* the live snapshot will arrive over the browser stream */ }
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
  function chooseBookPrice(level: BookLevel, intent: 'cross' | 'join') {
    slipPrice = level.price
    slipIntent = intent
    slipPolicy = intent === 'cross' ? 'limit' : 'post_only'
    slipCap = `${takerQuote(level).moneyline}`
    slipStatus = ''
    slipOpen = true
  }
  function setBookSide(side: 'yes' | 'no') { bookSide = side; slipOpen = false }
  async function submitOrder() {
    if (!snapshot.health.tradingEnabled) { slipStatus = 'Demo order entry is locked on this server.'; return }
    slipStatus = 'Submitting…'
    try {
      const response = await fetch('/api/parent-orders', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ eventId: selectedEvent?.id, rotation: selectedEvent?.participants.find(participant => participant.name === selectedQuote?.outcome)?.rotation || '', ticker: selectedQuote?.ticker, outcome: activeOutcome, market: marketLabel(selectedMarket), side: bookSide, strategy: slipStrategy, policy: slipPolicy, cashRisk: Math.round((Number(slipRisk) || 0) * 10000), priceCapMoneyline: Number(slipCap), limitPrice: slipPrice, sliceQuantity: Math.round((Number(slipSlice) || 0) * 10000) }) })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Order was rejected')
      slipStatus = `Parent order ${payload.id} created`
    } catch (cause) { slipStatus = cause instanceof Error ? cause.message : 'Unable to submit order' }
  }
  function parentForOrder(order: Order) {
    return snapshot.parentOrders.find(parent => parent.childOrderIds.includes(order.id))
  }
  function monitoredStatus(order: Order) {
    const parent = parentForOrder(order)
    return parent ? `${parent.strategy} · ${parent.status}${parent.strategy === 'follow' && parent.replaceCount ? ` · ${parent.replaceCount} reprices` : ''}` : order.status
  }
  async function cancelParent(parent: ParentOrder) {
    if (!snapshot.health.tradingEnabled || cancelingParentID) return
    cancelingParentID = parent.id
    try {
      const response = await fetch(`/api/parent-orders/${encodeURIComponent(parent.id)}`, { method: 'DELETE' })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Unable to cancel demo order')
      snapshot = { ...snapshot, parentOrders: [payload as ParentOrder, ...snapshot.parentOrders.filter(order => order.id !== parent.id)], orders: snapshot.orders.map(order => parent.childOrderIds.includes(order.id) ? { ...order, status: 'canceled', cashRisk: 0 } : order) }
    } catch (cause) {
      slipStatus = cause instanceof Error ? cause.message : 'Unable to cancel demo order'
    } finally {
      cancelingParentID = ''
    }
  }
  function applyStream(message: { type: string; data: unknown }) {
    if (message.type === 'account_snapshot') {
      const account = message.data as AccountSnapshot
      ;(account.fills || []).forEach(fill => seenFillIDs.add(fill.id))
      snapshot = { ...snapshot, parentOrders: account.parentOrders || [], orders: account.orders || [], positions: account.positions || [], fills: account.fills || [], bankroll: account.bankroll || 0, atRisk: account.atRisk || 0 }
    }
    if (message.type === 'schedule') snapshot = { ...snapshot, events: (message.data as Event[]) || [] }
    if (message.type === 'health') snapshot = { ...snapshot, health: message.data as Health }
    if (message.type === 'settings') { snapshot = { ...snapshot, settings: message.data as Settings }; draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name); draftExcludeAddedGames = snapshot.settings.preferences.excludeAddedGames }
    if (message.type === 'orderbook') { const next = normalizeBook(message.data as OrderBook); if (next.ticker === selectedQuote?.ticker) book = next }
    if (message.type === 'book_stale') { const next = normalizeBook(message.data as OrderBook); if (next.ticker === selectedQuote?.ticker) book = { ...next, stale: true } }
    if (message.type === 'fill') {
      const next = message.data as Fill
      snapshot = { ...snapshot, fills: [next, ...snapshot.fills.filter(fill => fill.id !== next.id)].slice(0, 250) }
      if (!seenFillIDs.has(next.id)) { seenFillIDs.add(next.id); notifyFill(next) }
    }
    if (message.type === 'order') { const next = message.data as Order; snapshot = { ...snapshot, orders: [next, ...snapshot.orders.filter(order => order.id !== next.id)] } }
    if (message.type === 'parent_order') { const next = message.data as ParentOrder; snapshot = { ...snapshot, parentOrders: [next, ...snapshot.parentOrders.filter(order => order.id !== next.id)] } }
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
    try { const response = await fetch('/api/snapshot'); if (!response.ok) throw new Error(`Server returned ${response.status}`); snapshot = normalizeSnapshot(await response.json()); snapshot.fills.forEach(fill => seenFillIDs.add(fill.id)); draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name); draftExcludeAddedGames = snapshot.settings.preferences.excludeAddedGames; connect() } catch (cause) { error = cause instanceof Error ? cause.message : 'Unable to load PMBattle' }
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
          <div class="market">{#if moneyline}{#each [moneyline.away, moneyline.home] as quote, index}<button class={index === 0 ? 'side-away' : 'side-home'} class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, moneyline)}><i class="side-tag">{index === 0 ? 'AWAY' : 'HOME'}</i><b>{ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Unavailable'}</small></button>{/each}{:else}<span>—</span><span>—</span>{/if}</div>
          <div class="market">{#if spreadMarket}{#each [spreadMarket.away, spreadMarket.home] as quote, index}<button class={index === 0 ? 'side-away' : 'side-home'} class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, spreadMarket)}><i class="side-tag">{index === 0 ? 'AWAY' : 'HOME'}</i><b>{spread(spreadMarket.line, index === 0)} · {ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Unavailable'}</small></button>{/each}{:else}<span>—</span><span>—</span>{/if}</div>
          <div class="market">{#if total}{#each [total.over, total.under] as quote, index}<button class={index === 0 ? 'side-over' : 'side-under'} class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, total)}><i class="side-tag">{index === 0 ? 'OVER' : 'UNDER'}</i><b>{index === 0 ? 'O' : 'U'} {total.line} · {ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Unavailable'}</small></button>{/each}{:else}<span>—</span><span>—</span>{/if}</div>
          <div class="start"><b>{day(event.startTime)}</b><span>{time(event.startTime)}</span><small>{expandedEventID === event.id ? 'Close ▲' : 'Book ▼'}</small></div>
        </section>
        {#if expandedEventID === event.id && selectedEvent?.id === event.id && selectedQuote && selectedMarket}
          <section class="inline-book" aria-label="Live order book">
            <header class="book-toolbar">
              <div class="book-market-title" class:role-away={activeRole === 'away'} class:role-home={activeRole === 'home'} class:role-over={activeRole === 'over'} class:role-under={activeRole === 'under'}><small><i class="selection-role">{activeRole.toUpperCase()}</i>{marketLabel(selectedMarket)}</small><h2>{activeOutcome}{selectedMarket.line ? ` ${selectedMarket.line}` : ''}</h2></div>
              {#if selectedMarket.options?.length}
                <div class="strikes" aria-label="Market lines">
                  {#each selectedMarket.options as option}<button class:active={option.line === selectedMarket.line} on:click={() => selectOption(option)}>{option.line}</button>{/each}
                </div>
              {/if}
              <div class="top-quote"><b>{ml(activeMoneyline)}</b><small>fee included</small></div>
              <div class="book-state" class:stale={!book || book.stale}><i></i>{!book ? 'CONNECTING' : book.stale ? 'STALE' : 'LIVE'}</div>
            </header>
            <nav class="trade-tabs"><button class:active={bookSide === 'yes'} on:click={() => setBookSide('yes')}>Trade Yes</button><button class:active={bookSide === 'no'} on:click={() => setBookSide('no')}>Trade No</button><span>{snapshot.health.tradingEnabled ? 'DEMO ORDERS' : 'READ-ONLY'}</span></nav>
            <div class="ladder-head"><span></span><span>Price <small>raw → fee included</small></span><span>Contracts</span><span>Total</span></div>
            {#if book && (displayAsks.length || displayBids.length)}
              <div class="ladder asks">
                {#each [...displayAsks].reverse() as level}
                  <button class="ladder-row" title="Use this ask in the order slip" on:click={() => chooseBookPrice(level, 'cross')} style={`--depth:${Math.min(100, Number(level.quantity) / Math.max(1, ...displayAsks.map(value => Number(value.quantity))) * 100)}%`}><b>ASK</b><span>{levelPrice(level)}</span><span>{qty(level.quantity)}</span><span>{money(takerQuote(level).cost)}</span></button>
                {/each}
              </div>
              <div class="book-center" class:role-away={activeRole === 'away'} class:role-home={activeRole === 'home'} class:role-over={activeRole === 'over'} class:role-under={activeRole === 'under'}><b>{activeRole.toUpperCase()} · Trade {bookSide === 'yes' ? 'Yes' : 'No'}</b><span>{activeOutcome}</span></div>
              <div class="ladder bids">
                {#each displayBids as level}
                  <button class="ladder-row" title="Join this bid in the order slip" on:click={() => chooseBookPrice(level, 'join')} style={`--depth:${Math.min(100, Number(level.quantity) / Math.max(1, ...displayBids.map(value => Number(value.quantity))) * 100)}%`}><b>BID</b><span>{levelPrice(level)}</span><span>{qty(level.quantity)}</span><span>{money(takerQuote(level).cost)}</span></button>
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
        {#each snapshot.orders as order}<div class="table-row compact"><span><b>#{order.rotation} {order.market}</b><small>{order.ticker}</small></span><span>{order.exchange}</span><span>{qty(order.quantity)}</span><span>{money(order.limitPrice)}</span><span>{monitoredStatus(order)}</span><span>—</span><span class="order-risk">{money(order.cashRisk)}{#if snapshot.health.tradingEnabled && parentForOrder(order) && workingOrders.includes(order)}<button class="cancel-order" disabled={Boolean(cancelingParentID)} on:click={() => cancelParent(parentForOrder(order)!)}>{cancelingParentID === parentForOrder(order)?.id ? 'Canceling…' : 'Cancel'}</button>{/if}</span></div>{:else}<div class="empty">No pending orders</div>{/each}
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
  <aside class="order-monitor" class:open={monitorOpen} aria-label="Order monitor">
    <button class="monitor-toggle" on:click={toggleMonitor} aria-expanded={monitorOpen}>
      <span class="monitor-live"><i></i>ORDERS</span>
      <b>{workingOrders.length} working</b>
      {#if unreadFills}<em>{unreadFills} new fill{unreadFills === 1 ? '' : 's'}</em>{:else}<small>{snapshot.fills[0] ? `Last fill ${time(snapshot.fills[0].createdAt)}` : 'Monitoring fills'}</small>{/if}
      <span>{monitorOpen ? '▼' : '▲'}</span>
    </button>
    {#if monitorOpen}
      <div class="monitor-body">
        <header><b>Working orders</b><button on:click={() => showActivity('orders')}>Full orders</button></header>
        {#each workingOrders.slice(0, 5) as order}
          <div class="monitor-row"><span><b>{order.market || order.ticker}</b><small>{order.exchange} · {monitoredStatus(order)}</small></span><span><b>{qty(Math.max(0, order.quantity - order.filledQuantity))}</b><small>remaining</small></span>{#if snapshot.health.tradingEnabled && parentForOrder(order)}<button class="cancel-order" disabled={Boolean(cancelingParentID)} on:click={() => cancelParent(parentForOrder(order)!)}>{cancelingParentID === parentForOrder(order)?.id ? '…' : 'Cancel'}</button>{/if}</div>
        {:else}<div class="monitor-empty">No working orders</div>{/each}
        <header><b>Recent fills</b><button on:click={() => showActivity('fills')}>Full fills</button></header>
        {#each snapshot.fills.slice(0, 3) as fill}
          <div class="monitor-row fill"><span><b>{fillName(fill)}</b><small>{time(fill.createdAt)} · {fill.exchange}</small></span><span><b>{qty(fill.quantity)}</b><small>{ml(fill.allInMoneyline)}</small></span></div>
        {:else}<div class="monitor-empty">No fills received</div>{/each}
      </div>
    {/if}
  </aside>
  <section class="fill-notices" aria-live="assertive" aria-label="Fill notifications">
    {#each fillNotices as notice (notice.key)}
      <article class="fill-notice"><i></i><div><small>FILL RECEIVED</small><b>{fillName(notice.fill)}</b><span>{qty(notice.fill.quantity)} contracts · {ml(notice.fill.allInMoneyline)} all-in</span></div><button on:click={() => viewFill(notice.key)}>View</button><button class="notice-close" aria-label="Dismiss fill notification" on:click={() => dismissNotice(notice.key)}>×</button></article>
    {/each}
  </section>
  {#if slipOpen && selectedQuote && selectedEvent}
    <aside class="order-slip" aria-label="Order slip">
      <header class:role-away={activeRole === 'away'} class:role-home={activeRole === 'home'} class:role-over={activeRole === 'over'} class:role-under={activeRole === 'under'}><div><small>ORDER SLIP · KALSHI · <i class="selection-role">{activeRole.toUpperCase()}</i></small><b>{activeOutcome} {selectedMarket?.line || ''}</b></div><button aria-label="Close order slip" on:click={() => slipOpen = false}>×</button></header>
      <div class="slip-price"><span>{slipIntent === 'cross' ? `Buy ${bookSide.toUpperCase()}` : `Join ${bookSide.toUpperCase()} bid`}</span><b>{ml(rawML(slipPrice))} <i>→ {ml(slipQuote?.moneyline)}</i></b><small>raw → fee included</small></div>
      <div class="slip-strategies"><button class:active={slipStrategy === 'basic'} on:click={() => slipStrategy = 'basic'}>Basic</button><button class:active={slipStrategy === 'iceberg'} on:click={() => slipStrategy = 'iceberg'}>Iceberg</button><button class:active={slipStrategy === 'follow'} on:click={() => { slipStrategy = 'follow'; slipPolicy = 'post_only' }}>Follow</button></div>
      <div class="slip-fields">
        <label><span>Cash at risk</span><div><i>$</i><input type="number" min="1" step="1" bind:value={slipRisk} /></div></label>
        <label><span>Worst all-in price</span><input type="number" step="1" bind:value={slipCap} /></label>
        <label><span>Order behavior</span><select bind:value={slipPolicy} disabled={slipStrategy === 'follow'}><option value="limit">Limit</option><option value="post_only">Post only</option><option value="ioc">Immediate or cancel</option></select></label>
        {#if slipStrategy === 'iceberg'}<label><span>Visible contracts</span><input type="number" min="1" step="1" bind:value={slipSlice} /></label>{/if}
      </div>
      <div class="slip-summary"><span>Estimated contracts <b>{qty(slipQuantity)}</b></span><span>Fee-adjusted cap <b>{ml(Number(slipCap))}</b></span></div>
      {#if slipStrategy === 'follow'}<p class="slip-status">Joins the live top bid, stays post-only, and pauses at your all-in cap or on stale data.</p>{/if}
      <button class="submit-order" disabled={!snapshot.health.tradingEnabled || !slipPrice || Number(slipRisk) <= 0} on:click={submitOrder}>{snapshot.health.tradingEnabled ? 'Place demo order' : 'Demo trading locked'}</button>
      {#if slipStatus}<p class="slip-status" aria-live="polite">{slipStatus}</p>{/if}
    </aside>
  {/if}
</div>
