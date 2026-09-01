<script lang="ts">
  import { onMount } from 'svelte'
  import type { AccountSnapshot, AccountSummary, AuditPage, AuditRecord, BookLevel, Event, Fill, Health, MappingReview, MarketOption, MarketView, Order, OrderBook, ParentOrder, Position, PriceQuote, Settings, Snapshot } from './types'

  let snapshot: Snapshot = { events: [], parentOrders: [], orders: [], positions: [], settlements: [], fills: [], health: { status: 'starting', mode: 'simulated', scheduleUpdated: '', exchangeState: 'disconnected', accountState: 'pending', mappedMarkets: 0, latencyMs: 0, tradingEnabled: false }, bankroll: 0, availableToAllocate: 0, atRisk: 0, settings: { preferences: { enabledSports: null, excludeAddedGames: false }, availableSports: [] } }
  let view: 'schedule' | 'settings' = 'schedule'
  let draftSports: string[] = []
  let draftExcludeAddedGames = false
  let settingsStatus = ''
  let mappingReviews: MappingReview[] = []
  let mappingLoaded = false
  let mappingLoading = false
  let mappingError = ''
  let mappingQuery = ''
  let mappingSelections: Record<string, string> = {}
  let mappingDeciding = ''
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
  let resumingParentID = ''
  let cancelGroupScope = 'all'
  let cancelingGroup = false
  let cancelGroupStatus = ''
  let unreadFills = 0
  let fillNotices: { key: string; fill: Fill }[] = []
  const seenFillIDs = new Set<string>()
  let book: OrderBook | null = null
  let trayOpen = false
  let trayTab: 'orders' | 'positions' | 'fills' | 'history' = 'fills'
  let historyMode: 'settlements' | 'audit' = 'settlements'
  let auditRecords: AuditRecord[] = []
  let auditNextBefore = 0
  let auditHasMore = false
  let auditLoading = false
  let auditError = ''
  let theme: 'light' | 'dark' = (localStorage.getItem('pmbattle-theme') as 'light' | 'dark') || 'dark'
  let error = ''

  const rawFetch = globalThis.fetch.bind(globalThis)
  // Every API call carries a custom header so a cross-site page cannot forge requests.
  function api(input: string, init: RequestInit = {}) {
    const headers = new Headers(init.headers || {})
    headers.set('X-Requested-With', 'PMBattle')
    return rawFetch(input, { ...init, headers })
  }

  const money = (value: number) => `$${(value / 10000).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
  const qty = (value: number) => (value / 10000).toLocaleString(undefined, { maximumFractionDigits: 2 })
  const ml = (value?: number) => value === undefined ? '—' : value > 0 ? `+${value}` : `${value}`
  const rawML = (price: number) => Math.round(price < 5000 ? 100 * (10000 - price) / price : -100 * price / (10000 - price))
  const dateKey = (value: string) => new Date(value).toISOString().slice(0, 10)
  const time = (value: string) => new Date(value).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
  const day = (value: string) => new Date(value).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })
  const updated = (value?: string) => value ? new Date(value).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' }) : 'Waiting'
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
  $: slipRiskMoney = Math.round((Number(slipRisk) || 0) * 10000)
  $: slipOverCap = Boolean(snapshot.health.maxCashRisk) && slipRiskMoney > (snapshot.health.maxCashRisk || 0)
  $: workingOrders = snapshot.orders.filter(order => !['canceled', 'cancelled', 'executed', 'filled', 'closed', 'rejected'].includes((order.status || '').toLowerCase()))
  $: activeParents = snapshot.parentOrders.filter(parent => !['canceled', 'cancelled', 'executed', 'filled', 'closed', 'rejected'].includes((parent.status || '').toLowerCase()))
  $: filteredMappingReviews = mappingReviews.filter(review => `${review.title} ${review.exchange} ${review.tickers.join(' ')} ${review.candidates.flatMap(candidate => candidate.participants.map(participant => `${participant.rotation} ${participant.name} ${participant.abbreviation}`)).join(' ')}`.toLowerCase().includes(mappingQuery.toLowerCase()))

  function normalizeSnapshot(value: Snapshot): Snapshot {
    return { ...value, events: value.events || [], parentOrders: value.parentOrders || [], orders: value.orders || [], positions: value.positions || [], settlements: value.settlements || [], fills: value.fills || [], availableToAllocate: value.availableToAllocate ?? value.bankroll ?? 0, settings: { ...value.settings, availableSports: value.settings?.availableSports || [] } }
  }
  function normalizeBook(value: OrderBook): OrderBook { return { ...value, yes: value.yes || [], no: value.no || [] } }
  // Account rows show the game and the outcome, never the raw exchange
  // ticker. The ticker stays available as the row's hover text.
  type NamedRow = { game?: string; outcome?: string; market?: string; ticker: string }
  const rowGame = (row: NamedRow) => row.game || row.ticker
  const rowDetail = (row: NamedRow) => [row.outcome, row.market].filter(Boolean).join(' · ') || row.ticker
  function fillName(fill: Fill) { return [fill.team, fill.market].filter(Boolean).join(' · ') || fill.ticker }
  function ago(value: string) {
    const at = new Date(value).getTime()
    if (!Number.isFinite(at)) return ''
    const seconds = Math.max(0, Math.round((Date.now() - at) / 1000))
    if (seconds < 60) return `${seconds}s ago`
    if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`
    if (seconds < 86400) return `${Math.round(seconds / 3600)}h ago`
    return day(value)
  }
  // One glanceable chip per order instead of raw words like partially_filled.
  const statusLabels: Record<string, { label: string; tone: string }> = {
    filled: { label: 'Filled', tone: 'done' }, executed: { label: 'Filled', tone: 'done' }, closed: { label: 'Filled', tone: 'done' },
    partially_filled: { label: 'Partial', tone: 'live' },
    canceled: { label: 'Canceled', tone: 'off' }, cancelled: { label: 'Canceled', tone: 'off' },
    rejected: { label: 'Rejected', tone: 'alert' },
    paused: { label: 'Paused', tone: 'alert' }, paused_stale: { label: 'Stale book', tone: 'alert' },
    price_capped: { label: 'Price capped', tone: 'alert' }, risk_capped: { label: 'Risk capped', tone: 'alert' },
    waiting_for_book: { label: 'Waiting', tone: 'alert' },
    resting: { label: 'Working', tone: 'live' }, working: { label: 'Working', tone: 'live' },
    submitting: { label: 'Sending', tone: 'live' }, submitted: { label: 'Sending', tone: 'live' },
    repricing: { label: 'Repricing', tone: 'live' }, refreshing: { label: 'Refreshing', tone: 'live' },
    risk_capping: { label: 'Capping', tone: 'alert' }, awaiting_fill: { label: 'Working', tone: 'live' },
  }
  function orderStatus(order: Order) {
    const parent = parentForOrder(order)
    const raw = (parent?.status || order.status || '').toLowerCase().trim()
    return statusLabels[raw] || { label: raw.replace(/_/g, ' ') || 'Unknown', tone: 'live' }
  }
  function orderNote(order: Order) {
    const parent = parentForOrder(order)
    if (!parent) return ''
    return parent.strategy === 'follow' && parent.replaceCount ? `${parent.strategy} · ${parent.replaceCount} reprices` : parent.strategy
  }
  function dismissNotice(key: string) { fillNotices = fillNotices.filter(notice => notice.key !== key) }
  function notifyFill(fill: Fill) {
    const key = fill.id || `${fill.ticker}-${Date.now()}`
    fillNotices = [{ key, fill }, ...fillNotices.filter(notice => notice.key !== key)].slice(0, 3)
    unreadFills += 1
    setTimeout(() => dismissNotice(key), 12000)
  }
  function viewFill(key: string) { dismissNotice(key); trayTab = 'fills'; trayOpen = true; slipOpen = false; unreadFills = 0 }
  async function showHistory(mode: 'settlements' | 'audit') {
    trayTab = 'history'; trayOpen = true; historyMode = mode
    if (mode === 'audit' && auditRecords.length === 0) await loadAudit(true)
  }
  async function loadAudit(reset = false) {
    if (auditLoading) return
    auditLoading = true; auditError = ''
    try {
      const before = reset ? 0 : auditNextBefore
      const response = await api(`/api/audit?limit=100${before ? `&before=${before}` : ''}`)
      if (!response.ok) throw new Error('Unable to load audit history')
      const page = await response.json() as AuditPage
      auditRecords = reset ? (page.records || []) : [...auditRecords, ...(page.records || [])]
      auditNextBefore = page.nextBefore || 0; auditHasMore = page.hasMore
    } catch (cause) { auditError = cause instanceof Error ? cause.message : 'Unable to load audit history' }
    finally { auditLoading = false }
  }
  const auditTitle = (kind: string) => kind.replaceAll('_', ' ')
  function auditSummary(record: AuditRecord) {
    const payload = record.payload as Record<string, any>
    const item = payload.parent || payload.request || payload.fill || payload
    return [item.id || item.parentId, item.ticker, item.strategy, item.status, payload.error || payload.strategy_error].filter(Boolean).join(' · ') || 'Recorded state change'
  }

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
      const response = await api('/api/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabledSports: draftSports, excludeAddedGames: draftExcludeAddedGames }) })
      if (!response.ok) throw new Error('Unable to save sports preferences')
      snapshot = normalizeSnapshot(await response.json()); draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name); selectedSport = 'ALL'; selectedLeague = 'ALL'; selectedDate = 'ALL'; settingsStatus = 'Saved'
    } catch (cause) { settingsStatus = cause instanceof Error ? cause.message : 'Unable to save settings' }
  }
  async function loadMappingReviews() {
    if (mappingLoading) return
    mappingLoading = true; mappingError = ''
    try {
      const response = await api('/api/mapping-reviews?limit=250')
      if (!response.ok) throw new Error('Unable to load mapping reviews')
      mappingReviews = await response.json() as MappingReview[]
      mappingSelections = Object.fromEntries(mappingReviews.filter(review => review.candidates[0]).map(review => [review.id, review.candidates[0].eventId]))
      mappingLoaded = true
    } catch (cause) { mappingError = cause instanceof Error ? cause.message : 'Unable to load mapping reviews' }
    finally { mappingLoading = false }
  }
  function candidateLabel(review: MappingReview, eventId: string) {
    const candidate = review.candidates.find(value => value.eventId === eventId)
    return candidate ? candidate.participants.map(participant => `${participant.rotation ? `#${participant.rotation} ` : ''}${participant.name}`).join(' at ') : 'this schedule game'
  }
  async function decideMapping(review: MappingReview, reject = false) {
    const eventId = mappingSelections[review.id]
    const action = reject ? 'reject this Kalshi market group' : `map this group to ${candidateLabel(review, eventId)}`
    if (!confirm(`Confirm: ${action}? This changes only PMBattle's local mapping and never places an order.`)) return
    mappingDeciding = review.id; mappingError = ''
    try {
      const response = await api(`/api/mapping-reviews/${encodeURIComponent(review.id)}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(reject ? { reject: true } : { eventId }) })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Unable to save mapping decision')
      mappingReviews = mappingReviews.filter(value => value.id !== review.id)
    } catch (cause) { mappingError = cause instanceof Error ? cause.message : 'Unable to save mapping decision' }
    finally { mappingDeciding = '' }
  }
  async function select(event: Event, quote?: PriceQuote, market?: MarketView) {
    if (!quote) return
    selectedEvent = event; selectedQuote = quote; selectedMarket = market || event.markets?.find(value => [value.away, value.home, value.over, value.under].some(valueQuote => valueQuote?.ticker === quote.ticker)) || null; bookSide = quote.side || 'yes'; expandedEventID = event.id; slipOpen = false; book = null
    try { const response = await api(`/api/books/${encodeURIComponent(quote.ticker)}`); if (response.ok) book = normalizeBook(await response.json()) } catch { /* the live snapshot will arrive over the browser stream */ }
  }
  function toggleGame(event: Event) {
    if (expandedEventID === event.id) { if (selectedQuote) api(`/api/books/${encodeURIComponent(selectedQuote.ticker)}`, { method: 'DELETE' }).catch(() => {}); expandedEventID = ''; selectedEvent = null; selectedQuote = null; selectedMarket = null; book = null; return }
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
    trayOpen = false
    slipOpen = true
  }
  function setBookSide(side: 'yes' | 'no') { bookSide = side; slipOpen = false }
  async function submitOrder() {
	if (!snapshot.health.tradingEnabled) { slipStatus = 'Order entry is locked on this server.'; return }
	if (snapshot.health.mode === 'live' && !confirm(`Place a REAL Kalshi order for ${activeOutcome} with ${money(Math.round((Number(slipRisk) || 0) * 10000))} cash at risk?`)) return
    slipStatus = 'Submitting…'
    try {
      const response = await api('/api/parent-orders', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ eventId: selectedEvent?.id, rotation: selectedEvent?.participants.find(participant => participant.name === selectedQuote?.outcome)?.rotation || '', ticker: selectedQuote?.ticker, outcome: activeOutcome, market: marketLabel(selectedMarket), side: bookSide, strategy: slipStrategy, policy: slipPolicy, cashRisk: Math.round((Number(slipRisk) || 0) * 10000), priceCapMoneyline: Number(slipCap), limitPrice: slipPrice, sliceQuantity: Math.round((Number(slipSlice) || 0) * 10000) }) })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Order was rejected')
      slipStatus = `Parent order ${payload.id} created`
    } catch (cause) { slipStatus = cause instanceof Error ? cause.message : 'Unable to submit order' }
  }
  function parentForOrder(order: Order) {
    return snapshot.parentOrders.find(parent => parent.childOrderIds.includes(order.id))
  }
  const canResume = (parent: ParentOrder | undefined) => parent?.strategy === 'follow' && parent.status?.toLowerCase() === 'paused'
  async function resumeParent(parent: ParentOrder) {
    if (!snapshot.health.tradingEnabled || resumingParentID) return
    resumingParentID = parent.id
    try {
      const response = await api(`/api/parent-orders/${encodeURIComponent(parent.id)}/resume`, { method: 'POST' })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Unable to resume follow order')
      snapshot = { ...snapshot, parentOrders: [payload as ParentOrder, ...snapshot.parentOrders.filter(order => order.id !== parent.id)] }
      cancelGroupStatus = 'Follow order resumed after fresh-book checks.'
    } catch (cause) {
      cancelGroupStatus = cause instanceof Error ? cause.message : 'Unable to resume follow order'
    } finally {
      resumingParentID = ''
    }
  }
  async function cancelParent(parent: ParentOrder) {
    if (!snapshot.health.tradingEnabled || cancelingParentID) return
    cancelingParentID = parent.id
    try {
      const response = await api(`/api/parent-orders/${encodeURIComponent(parent.id)}`, { method: 'DELETE' })
      const payload = await response.json()
		if (!response.ok) throw new Error(payload.error || 'Unable to cancel order')
      snapshot = { ...snapshot, parentOrders: [payload as ParentOrder, ...snapshot.parentOrders.filter(order => order.id !== parent.id)], orders: snapshot.orders.map(order => parent.childOrderIds.includes(order.id) ? { ...order, status: 'canceled', cashRisk: 0 } : order) }
    } catch (cause) {
		slipStatus = cause instanceof Error ? cause.message : 'Unable to cancel order'
    } finally {
      cancelingParentID = ''
    }
  }
  async function cancelGroup() {
    if (!snapshot.health.tradingEnabled || cancelingGroup) return
    let scope = cancelGroupScope
    let value = ''
    if (cancelGroupScope.includes(':')) [scope, value] = cancelGroupScope.split(':', 2)
    if (scope === 'event') value = selectedEvent?.id || ''
    if (scope === 'event' && !value) { cancelGroupStatus = 'Open a game before canceling its orders.'; return }
    cancelingGroup = true
    cancelGroupStatus = 'Canceling…'
    try {
      const response = await api('/api/parent-orders/cancel', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ scope, value }) })
      const payload = await response.json()
      if (!response.ok && response.status !== 207) throw new Error(payload.error || 'Unable to cancel managed orders')
      const canceled = (payload.canceled || []) as ParentOrder[]
      const byID = new Map(canceled.map(parent => [parent.id, parent]))
      const canceledChildren = new Set(canceled.flatMap(parent => parent.childOrderIds))
      snapshot = { ...snapshot, parentOrders: snapshot.parentOrders.map(parent => byID.get(parent.id) || parent), orders: snapshot.orders.map(order => canceledChildren.has(order.id) ? { ...order, status: 'canceled', cashRisk: 0 } : order) }
      cancelGroupStatus = payload.failures?.length ? `${canceled.length} canceled · ${payload.failures.length} failed` : `${canceled.length} managed order${canceled.length === 1 ? '' : 's'} canceled`
    } catch (cause) {
      cancelGroupStatus = cause instanceof Error ? cause.message : 'Unable to cancel managed orders'
    } finally {
      cancelingGroup = false
    }
  }
  function applyStream(message: { type: string; data: unknown }) {
    if (message.type === 'account_snapshot') {
      const account = message.data as AccountSnapshot
      ;(account.fills || []).forEach(fill => seenFillIDs.add(fill.id))
      snapshot = { ...snapshot, parentOrders: account.parentOrders || [], orders: account.orders || [], positions: account.positions || [], settlements: account.settlements || [], fills: account.fills || [], bankroll: account.bankroll || 0, availableToAllocate: account.availableToAllocate ?? account.bankroll ?? 0, atRisk: account.atRisk || 0 }
    }
    if (message.type === 'account_summary') { const summary = message.data as AccountSummary; snapshot = { ...snapshot, bankroll: summary.bankroll, availableToAllocate: summary.availableToAllocate, atRisk: summary.atRisk } }
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
    try { const response = await api('/api/snapshot'); if (!response.ok) throw new Error(`Server returned ${response.status}`); snapshot = normalizeSnapshot(await response.json()); snapshot.fills.forEach(fill => seenFillIDs.add(fill.id)); draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name); draftExcludeAddedGames = snapshot.settings.preferences.excludeAddedGames; connect() } catch (cause) { error = cause instanceof Error ? cause.message : 'Unable to load PMBattle' }
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
    <span class="account">Available <b>{money(snapshot.bankroll)}</b> · New orders <b>{money(snapshot.availableToAllocate)}</b> · At risk <b>{money(snapshot.atRisk)}</b></span>
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
          <div class="teams">{#each event.participants as participant}<div><strong>{participant.name}</strong></div>{/each}</div>
          <div class="market">{#if moneyline}{#each [moneyline.away, moneyline.home] as quote, index}<button class={index === 0 ? 'side-away' : 'side-home'} class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, moneyline)}><i class="side-tag">{index === 0 ? 'AWAY' : 'HOME'}</i><b>{ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Listed · no offer'}</small></button>{/each}{:else}<span class="market-unlisted" title="Kalshi has not listed a matching moneyline market">Not listed</span>{/if}</div>
          <div class="market">{#if spreadMarket}{#each [spreadMarket.away, spreadMarket.home] as quote, index}<button class={index === 0 ? 'side-away' : 'side-home'} class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, spreadMarket)}><i class="side-tag">{index === 0 ? 'AWAY' : 'HOME'}</i><b>{spread(spreadMarket.line, index === 0)} · {ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Listed · no offer'}</small></button>{/each}{:else}<span class="market-unlisted" title="Kalshi has not listed a matching spread market">Not listed</span>{/if}</div>
          <div class="market">{#if total}{#each [total.over, total.under] as quote, index}<button class={index === 0 ? 'side-over' : 'side-under'} class:selected={selectedQuote?.ticker === quote?.ticker} disabled={!quote} on:click|stopPropagation={() => select(event, quote, total)}><i class="side-tag">{index === 0 ? 'OVER' : 'UNDER'}</i><b>{index === 0 ? 'O' : 'U'} {total.line} · {ml(quote?.allInMoneyline)}</b><small>{quote ? `${quote.exchange} · ${money(quote.availableQuantity)}` : 'Listed · no offer'}</small></button>{/each}{:else}<span class="market-unlisted" title="Kalshi has not listed a matching total market">Not listed</span>{/if}</div>
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
			<nav class="trade-tabs"><button class:active={bookSide === 'yes'} on:click={() => setBookSide('yes')}>Trade Yes</button><button class:active={bookSide === 'no'} on:click={() => setBookSide('no')}>Trade No</button><span>{snapshot.health.tradingEnabled ? snapshot.health.mode === 'live' ? 'REAL ORDERS' : 'DEMO ORDERS' : 'READ-ONLY'}</span></nav>
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
    <div class="tray-tabs" aria-label="Account activity">
      <button class:active={trayTab === 'positions'} on:click={() => { trayTab = 'positions'; trayOpen = true }}>Positions ({snapshot.positions.length})</button>
      <button class:active={trayTab === 'orders'} on:click={() => { trayTab = 'orders'; trayOpen = true }}>Orders ({snapshot.orders.length})</button>
      <button class:active={trayTab === 'fills'} on:click={() => { trayTab = 'fills'; trayOpen = true; unreadFills = 0 }}>Fills ({unreadFills ? `${unreadFills} new` : snapshot.fills.length})</button>
      <button class:active={trayTab === 'history'} on:click={() => showHistory('settlements')}>History ({snapshot.settlements.length})</button>
      <span class="tray-status"><b>{workingOrders.length} working</b><small>{snapshot.fills[0] ? `Last fill ${time(snapshot.fills[0].createdAt)}` : 'Monitoring fills'}</small></span>
      <button class="tray-toggle" on:click={() => trayOpen = !trayOpen}>{trayOpen ? 'Collapse ↓' : 'Open ↑'}</button>
    </div>
    {#if trayOpen}<div class="tray-body">
      {#if trayTab === 'fills'}
        <div class="table-head"><span>Game / bet</span><span>Exchange</span><span class="num">Quantity</span><span class="num">Raw</span><span class="num">All-in</span><span class="num">Fee</span><span class="num">Cash risk</span></div>
        {#each snapshot.fills as fill}<div class="table-row" title={fill.ticker}><span><b>{rowGame(fill)}</b><small>{fillName(fill)} · {ago(fill.createdAt)}</small></span><span>{fill.exchange}</span><span class="num">{qty(fill.quantity)}</span><span class="num">{money(fill.rawPrice)}</span><span class="num">{ml(fill.allInMoneyline)}</span><span class="num">{money(fill.fee)}</span><span class="num">{money(fill.cashRisk)}</span></div>{:else}<div class="empty">No fills yet</div>{/each}
      {:else if trayTab === 'orders'}
        {#if snapshot.health.tradingEnabled}<div class="cancel-scope-bar"><b>{snapshot.health.mode === 'live' ? 'Real-order kill switch' : 'Demo kill switch'}</b><select bind:value={cancelGroupScope} aria-label="Cancel scope"><option value="all">All managed orders</option><option value="event" disabled={!selectedEvent}>Current game</option><option value="strategy:basic">Basic orders</option><option value="strategy:iceberg">Iceberg orders</option><option value="strategy:follow">Follow orders</option><option value="exchange:Kalshi">Kalshi managed orders</option></select><button disabled={cancelingGroup || activeParents.length === 0} on:click={cancelGroup}>{cancelingGroup ? 'Canceling…' : 'Cancel scope'}</button><small aria-live="polite">{cancelGroupStatus}</small></div>{/if}
        <div class="table-head"><span>Game / bet</span><span>Exchange</span><span class="num">Quantity</span><span class="num">Limit</span><span>Status</span><span></span><span class="num">Cash risk</span></div>
        {#each snapshot.orders as order}<div class="table-row compact" title={order.ticker}><span><b>{rowGame(order)}</b><small>{rowDetail(order)}</small></span><span>{order.exchange}</span><span class="num">{qty(order.quantity)}</span><span class="num">{money(order.limitPrice)}</span><span><i class="pill {orderStatus(order).tone}">{orderStatus(order).label}</i>{#if orderNote(order)}<small>{orderNote(order)}</small>{/if}</span><span></span><span class="order-risk num">{money(order.cashRisk)}{#if snapshot.health.tradingEnabled && canResume(parentForOrder(order))}<button class="resume-order" disabled={Boolean(resumingParentID)} on:click={() => resumeParent(parentForOrder(order)!)}>{resumingParentID === parentForOrder(order)?.id ? 'Resuming…' : 'Resume'}</button>{/if}{#if snapshot.health.tradingEnabled && parentForOrder(order) && workingOrders.includes(order)}<button class="cancel-order" disabled={Boolean(cancelingParentID)} on:click={() => cancelParent(parentForOrder(order)!)}>{cancelingParentID === parentForOrder(order)?.id ? 'Canceling…' : 'Cancel'}</button>{/if}</span></div>{:else}<div class="empty">No pending orders</div>{/each}
      {:else if trayTab === 'positions'}
        <div class="table-head"><span>Game / bet</span><span>Exchange</span><span class="num">Contracts</span><span class="num">Exposure</span><span class="num">Traded</span><span class="num">Fees</span><span class="num">Realized P&amp;L</span></div>
        {#each snapshot.positions as position}<div class="table-row compact" title={position.ticker}><span><b>{rowGame(position)}</b><small>{rowDetail(position)}</small></span><span>{position.exchange}</span><span class="num">{qty(Math.abs(position.quantity))}</span><span class="num">{money(position.cashRisk)}</span><span class="num">{money(position.totalTraded || 0)}</span><span class="num">{money(position.feesPaid || 0)}</span><span class="num" class:positive={(position.realizedPnl || 0) >= 0} class:negative={(position.realizedPnl || 0) < 0}>{money(position.realizedPnl || 0)}</span></div>{:else}<div class="empty">No open positions</div>{/each}
      {:else}
        <div class="history-switch"><button class:active={historyMode === 'settlements'} on:click={() => showHistory('settlements')}>Settlements</button><button class:active={historyMode === 'audit'} on:click={() => showHistory('audit')}>System audit</button><span>{historyMode === 'audit' ? 'Loaded only when opened' : 'Exchange results'}</span></div>
        {#if historyMode === 'settlements'}
          <div class="table-head"><span>Game / bet</span><span>Exchange</span><span>Result</span><span class="num">Yes / No</span><span class="num">Revenue</span><span class="num">Fees</span><span class="num">Net P&amp;L</span></div>
          {#each snapshot.settlements as settlement}<div class="table-row compact" title={settlement.ticker}><span><b>{rowGame(settlement)}</b><small>{rowDetail(settlement)} · {day(settlement.settledAt)}</small></span><span>{settlement.exchange}</span><span>{settlement.result.toUpperCase()}</span><span class="num">{qty(settlement.yesQuantity)} / {qty(settlement.noQuantity)}</span><span class="num">{money(settlement.revenue)}</span><span class="num">{money(settlement.fee)}</span><span class="num" class:positive={settlement.netPnl >= 0} class:negative={settlement.netPnl < 0}>{money(settlement.netPnl)}</span></div>{:else}<div class="empty">No settled markets yet</div>{/each}
        {:else}
          <div class="audit-list">
            {#each auditRecords as record (record.id)}<article><span><b>{new Date(record.occurredAt).toLocaleString()}</b><small>Record #{record.id}</small></span><span><b>{auditTitle(record.kind)}</b><small>{auditSummary(record)}</small></span><details><summary>Details</summary><pre>{JSON.stringify(record.payload, null, 2)}</pre></details></article>{:else}{#if !auditLoading}<div class="empty">No order activity has been audited yet</div>{/if}{/each}
          </div>
          {#if auditError}<div class="error" role="alert">{auditError}</div>{/if}
          {#if auditLoading}<div class="audit-more">Loading audit history…</div>{:else if auditHasMore}<div class="audit-more"><button on:click={() => loadAudit(false)}>Load earlier records</button></div>{/if}
        {/if}
      {/if}
    </div>{/if}
  </section>
  {:else}
    <main class="settings-page">
      <header><h1>Settings</h1><p>Choose the sports PMBattle should load and subscribe to. Your choices are saved on this server.</p></header>
      <section class="settings-section safety-section">
		<div class="settings-heading"><div><h2>Trading status</h2><p>Order entry is controlled when the server starts.</p></div><span class="safety-badge" class:armed={snapshot.health.tradingEnabled}>{snapshot.health.tradingEnabled ? snapshot.health.mode === 'live' ? 'LIVE TRADING' : 'DEMO TRADING' : 'READ-ONLY'}</span></div>
        <div class="safety-grid">
          <span><small>Environment</small><b>{snapshot.health.mode.toUpperCase()}</b></span>
          <span><small>Exchange feed</small><b>{snapshot.health.exchangeState.toUpperCase()}</b></span>
          <span><small>Account sync</small><b>{(snapshot.health.accountState || 'pending').toUpperCase()}</b><i>{updated(snapshot.health.accountUpdated)}</i></span>
          <span><small>Mapped markets</small><b>{(snapshot.health.mappedMarkets || 0).toLocaleString()}</b></span>
          <span><small>Available to trade</small><b>{money(snapshot.bankroll)}</b></span>
          <span><small>New-order capacity</small><b>{money(snapshot.availableToAllocate)}</b></span>
          <span><small>Cash at risk</small><b>{money(snapshot.atRisk)}</b></span>
          <span><small>Per-order cap</small><b>{snapshot.health.maxCashRisk ? money(snapshot.health.maxCashRisk) : '—'}</b><i>PMBATTLE_MAX_CASH_RISK</i></span>
        </div>
        <p class="safety-note" class:armed={snapshot.health.tradingEnabled}>{snapshot.health.tradingEnabled ? snapshot.health.mode === 'live' ? 'REAL Kalshi orders are enabled on this server. Every submission asks for confirmation first. To lock it again, stop the server, set PMBATTLE_TRADING_ENABLED=false, and start it again.' : 'Kalshi demo order entry is enabled. Demo orders use play money only.' : snapshot.health.tradingLock || 'Order entry is locked.'}</p>
      </section>
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
      <section class="settings-section mapping-section">
        <div class="settings-heading"><div><h2>Market mapping review</h2><p>Ambiguous Kalshi markets stay hidden and cannot be traded until you approve an evidence-backed schedule match.</p></div><button class="load-mappings" disabled={mappingLoading} on:click={loadMappingReviews}>{mappingLoading ? 'Loading…' : mappingLoaded ? 'Refresh queue' : 'Load review queue'}</button></div>
        {#if mappingLoaded}
          <div class="mapping-tools"><input aria-label="Search mapping reviews" bind:value={mappingQuery} placeholder="Search team, rotation or ticker" /><span>{filteredMappingReviews.length} group{filteredMappingReviews.length === 1 ? '' : 's'}</span></div>
          <div class="mapping-list">
            {#each filteredMappingReviews as review (review.id)}
              <article class="mapping-card">
                <header><span><b>{review.title}</b><small>{review.exchange.toUpperCase()} · {review.tickers.length} contract{review.tickers.length === 1 ? '' : 's'} · {review.marketTypes.join(', ')}</small></span>{#if review.occurrenceTime}<time>{day(review.occurrenceTime)} · {time(review.occurrenceTime)}</time>{/if}</header>
                <div class="mapping-candidates">
                  {#each review.candidates as candidate}
                    <label><input type="radio" name={`mapping-${review.id}`} value={candidate.eventId} bind:group={mappingSelections[review.id]} /><span><b>{candidate.participants.map(participant => `${participant.rotation ? `#${participant.rotation} ` : ''}${participant.name}`).join(' at ')}</b><small>{candidate.sport} · {candidate.league} · {day(candidate.startTime)} {time(candidate.startTime)} · match {candidate.score}%</small></span></label>
                  {/each}
                </div>
                <footer><button class="reject-mapping" disabled={mappingDeciding === review.id} on:click={() => decideMapping(review, true)}>Reject group</button><button class="accept-mapping" disabled={!mappingSelections[review.id] || mappingDeciding === review.id} on:click={() => decideMapping(review)}>{mappingDeciding === review.id ? 'Saving…' : 'Accept selected game'}</button></footer>
              </article>
            {:else}<div class="empty">No matching review groups found.</div>{/each}
          </div>
        {:else}<div class="mapping-intro">The queue is loaded only when requested, keeping the main schedule fast.</div>{/if}
        {#if mappingError}<div class="error" role="alert">{mappingError}</div>{/if}
      </section>
    </main>
  {/if}
  <section class="fill-notices" aria-live="assertive" aria-label="Fill notifications">
    {#each fillNotices as notice (notice.key)}
      <article class="fill-notice"><i></i><div><small>FILL RECEIVED</small><b>{fillName(notice.fill)}</b><span>{rowGame(notice.fill)} · {qty(notice.fill.quantity)} @ {ml(notice.fill.allInMoneyline)}</span></div><button on:click={() => viewFill(notice.key)}>View</button><button class="notice-close" aria-label="Dismiss fill notification" on:click={() => dismissNotice(notice.key)}>×</button></article>
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
      {#if slipOverCap}<p class="slip-status" role="alert">Cash at risk is above this server's per-order cap of {money(snapshot.health.maxCashRisk || 0)}.</p>{/if}
      {#if slipStrategy === 'follow'}<p class="slip-status">Joins the live top bid, stays post-only, and pauses at your all-in cap or on stale data.</p>{/if}
	  <button class="submit-order" disabled={!snapshot.health.tradingEnabled || !slipPrice || Number(slipRisk) <= 0 || slipOverCap} on:click={submitOrder}>{snapshot.health.tradingEnabled ? snapshot.health.mode === 'live' ? 'Review & place real order' : 'Place demo order' : 'Trading locked'}</button>
      {#if slipStatus}<p class="slip-status" aria-live="polite">{slipStatus}</p>{/if}
    </aside>
  {/if}
</div>
