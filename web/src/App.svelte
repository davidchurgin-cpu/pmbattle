<script lang="ts">
	import { onMount, tick } from 'svelte'
  import type { AccountSnapshot, AccountSummary, AuditPage, AuditRecord, BookLevel, Event, Fill, Health, MappingReview, MarketOption, MarketView, Order, OrderBook, ParentOrder, Position, PriceQuote, Settings, Snapshot } from './types'

  let snapshot: Snapshot = { events: [], parentOrders: [], orders: [], positions: [], settlements: [], fills: [], health: { status: 'starting', mode: 'simulated', scheduleUpdated: '', exchangeState: 'disconnected', accountState: 'pending', mappedMarkets: 0, latencyMs: 0, tradingEnabled: false }, bankroll: 0, availableToAllocate: 0, atRisk: 0, settings: { preferences: { enabledSports: null, excludeAddedGames: false }, availableSports: [] } }
  type View = 'schedule' | 'orders' | 'positions' | 'fills' | 'history' | 'settings'
  let view: View = 'schedule'
  let draftSports: string[] = []
  let draftExcludeAddedGames = false
  let settingsStatus = ''
  let refreshingAccount = false
  let mappingReviews: MappingReview[] = []
  let mappingLoaded = false
  let mappingLoading = false
  let mappingError = ''
  let mappingQuery = ''
  let mappingSelections: Record<string, string> = {}
  let mappingDeciding = ''
  let query = ''
  let accountQuery = ''
  let accountStatus = 'active'
  let accountMarket = 'ALL'
  let accountStrategy = 'ALL'
  let expandedAccountRow = ''
  let accountDisplayLimit = 100
  let searchInput: HTMLInputElement
  let keyboardIndex = -1
  let selectedSport = 'ALL'
  let selectedLeague = 'ALL'
  let selectedDate = 'ALL'
  let gameDisplayLimit = 80
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
  let slipCapAuto = true
  let slipStrategy: 'basic' | 'iceberg' | 'follow' = 'basic'
  let slipPolicy: 'limit' | 'post_only' | 'ioc' = 'limit'
  let slipSlice = '25'
  let slipStatus = ''
  let submittingOrder = false
  let cancelingParentID = ''
  let cancelingOrderID = ''
  let editingOrderID = ''
  let editQuantity = ''
  let editLimit = ''
  let savingOrderID = ''
  let resumingParentID = ''
  let cancelGroupScope = 'all'
  let cancelingGroup = false
  let cancelGroupStatus = ''
  let unreadFills = 0
  let fillNotices: { key: string; fill: Fill }[] = []
  const seenFillIDs = new Set<string>()
  let book: OrderBook | null = null
  let historyMode: 'settlements' | 'orders' | 'audit' = 'settlements'
  let auditRecords: AuditRecord[] = []
  let auditNextBefore = 0
  let auditHasMore = false
  let auditLoading = false
  let auditError = ''
  let theme: 'light' | 'dark' = (localStorage.getItem('pmbattle-theme') as 'light' | 'dark') || 'dark'
  let error = ''
  let browserStreamState: 'connecting' | 'live' | 'reconnecting' = 'connecting'
  let streamSocket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectDelay = 1000
  let snapshotRetryTimer: ReturnType<typeof setTimeout> | null = null
  let snapshotRetryDelay = 1000
  let streamStopped = false

  const rawFetch = globalThis.fetch.bind(globalThis)
  // Every API call carries a custom header so a cross-site page cannot forge requests.
  function api(input: string, init: RequestInit = {}) {
    const headers = new Headers(init.headers || {})
    headers.set('X-Requested-With', 'PMBattle')
    return rawFetch(input, { ...init, headers })
  }

  const viewPath: Record<View, string> = { schedule: '/', orders: '/orders', positions: '/positions', fills: '/fills', history: '/history', settings: '/settings' }
  function pathView(path: string): View {
    const value = path.replace(/\/+$/, '') || '/'
    return (Object.entries(viewPath).find(([, route]) => route === value)?.[0] as View) || 'schedule'
  }
  function navigate(next: View, replace = false) {
    view = next
    const route = viewPath[next]
    if (window.location.pathname !== route) window.history[replace ? 'replaceState' : 'pushState']({}, '', route)
    if (next === 'fills') unreadFills = 0
    if (next === 'history' && historyMode === 'audit' && auditRecords.length === 0) void loadAudit(true)
    window.scrollTo({ top: 0 })
  }

  const money = (value: number) => `$${(value / 10000).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
  const qty = (value: number) => (value / 10000).toLocaleString(undefined, { maximumFractionDigits: 2 })
  const ml = (value?: number) => value === undefined ? '—' : value > 0 ? `+${value}` : `${value}`
  const rawML = (price: number) => Math.round(price < 5000 ? 100 * (10000 - price) / price : -100 * price / (10000 - price))
  const moneylineProbability = (value: number) => value >= 100 ? 10000 * 100 / (value + 100) : value <= -100 ? 10000 * -value / (-value + 100) : Number.NaN
  function positionOdds(position: Position) {
    const quantity = Math.abs(position.quantity)
    const raw = position.averagePrice || (quantity ? Math.round(position.cashRisk * 10000 / quantity) : 0)
    const allIn = quantity ? Math.min(9999, Math.round((position.cashRisk + (position.feesPaid || 0)) * 10000 / quantity)) : raw
    return raw > 0 && raw < 10000 ? `${ml(rawML(raw))} → ${ml(rawML(allIn))}` : '—'
  }
	const dateKey = (value: string | Date) => {
		const date = value instanceof Date ? value : new Date(value)
		return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
	}
  const time = (value: string) => new Date(value).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
  const day = (value: string) => new Date(value).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })
  const updated = (value?: string) => value ? new Date(value).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' }) : 'Waiting'
  const spread = (line: string | undefined, away: boolean) => {
    const value = Number(line || 0) * (away ? -1 : 1)
    return value > 0 ? `+${value}` : `${value}`
  }
  const ceilDiv = (value: bigint, divisor: bigint) => (value + divisor - 1n) / divisor
  const feeQuote = (level: BookLevel, rate: bigint) => {
    const d = 10000n, price = BigInt(level.price), quantity = BigInt(level.quantity)
    const fee = ceilDiv(rate * quantity * price * (d - price), d * d * d)
    const cost = ceilDiv(price * quantity, d) + fee
    const effective = Number(ceilDiv(cost * d, quantity))
    const moneyline = effective === 5000 ? 100 : effective < 5000 ? Math.round(100 * (10000 - effective) / effective) : -Math.round(100 * effective / (10000 - effective))
    return { fee: Number(fee), cost: Number(cost), moneyline }
  }
  const takerQuote = (level: BookLevel) => feeQuote(level, 700n)
  const makerQuote = (level: BookLevel) => feeQuote(level, 175n)
  function quantityForCashRisk(price: number, cashRisk: number) {
    if (price <= 0 || price >= 10000 || cashRisk <= 0) return 0
    let low = 0
    let high = Math.floor(cashRisk * 10000 / price) + 1
    while (low < high) {
      const mid = low + Math.floor((high - low + 1) / 2)
      if (takerQuote({ price, quantity: mid }).cost <= cashRisk) low = mid
      else high = mid - 1
    }
    return Math.floor(low / 100) * 100
  }
  const levelPrice = (level: BookLevel, maker = false) => {
    const quote = maker ? makerQuote(level) : takerQuote(level)
    return `${ml(rawML(level.price))} → ${ml(quote.moneyline)}`
  }
  function orderOdds(order: Order) {
    const remaining = Math.max(100, order.quantity - order.filledQuantity)
    return `${ml(rawML(order.limitPrice))} → ${ml(takerQuote({ price: order.limitPrice, quantity: remaining }).moneyline)}`
  }
  function ownOrdersAtLevel(level: BookLevel) {
    const matches = workingOrders.filter(order => order.ticker === selectedQuote?.ticker && order.side === bookSide && order.limitPrice === level.price)
    return { count: matches.length, quantity: matches.reduce((total, order) => total + Math.max(0, order.quantity - order.filledQuantity), 0) }
  }
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
  $: slipRiskMoney = Math.round((Number(slipRisk) || 0) * 10000)
  $: slipQuantity = quantityForCashRisk(slipPrice, slipRiskMoney)
  $: slipSliceQuantity = Math.round((Number(slipSlice) || 0) * 10000)
  $: slipQuote = slipPrice > 0 && slipQuantity > 0 ? takerQuote({ price: slipPrice, quantity: slipQuantity }) : null
  $: slipMakerQuote = slipPrice > 0 && slipQuantity > 0 ? makerQuote({ price: slipPrice, quantity: slipQuantity }) : null
  $: if (slipCapAuto && slipQuote) slipCap = `${slipQuote.moneyline}`
  $: slipCapInvalid = !Number.isFinite(moneylineProbability(Number(slipCap)))
  $: slipBeyondCap = Boolean(slipQuote && !slipCapInvalid && moneylineProbability(slipQuote.moneyline) > moneylineProbability(Number(slipCap)))
  $: slipInvalidSlice = slipStrategy === 'iceberg' && (slipSliceQuantity <= 0 || slipSliceQuantity >= slipQuantity)
  $: slipOverCap = Boolean(snapshot.health.maxCashRisk) && slipRiskMoney > (snapshot.health.maxCashRisk || 0)
  $: bookActionable = Boolean(book && !book.stale && browserStreamState === 'live')
  $: editQuantityValue = Math.round((Number(editQuantity) || 0) * 10000)
  $: editLimitValue = Math.round((Number(editLimit) || 0) * 100)
  $: editRiskQuote = editQuantityValue > 0 && editLimitValue > 0 && editLimitValue < 10000 ? takerQuote({ price: editLimitValue, quantity: editQuantityValue }) : null
  $: editOverCap = Boolean(editRiskQuote && snapshot.health.maxCashRisk && editRiskQuote.cost > (snapshot.health.maxCashRisk || 0))
  $: editingOrder = snapshot.orders.find(order => order.id === editingOrderID)
  $: editOverAvailable = Boolean(editRiskQuote && editingOrder && editRiskQuote.cost > snapshot.availableToAllocate + editingOrder.cashRisk)
  $: workingOrders = snapshot.orders.filter(order => !['canceled', 'cancelled', 'executed', 'filled', 'closed', 'rejected'].includes((order.status || '').toLowerCase()))
  $: activeParents = snapshot.parentOrders.filter(parent => !['canceled', 'cancelled', 'executed', 'filled', 'closed', 'rejected'].includes((parent.status || '').toLowerCase()))
  $: scopedCancelParents = activeParents.filter(parent => cancelGroupScope === 'event' ? parent.eventId === selectedEvent?.id : cancelGroupScope.startsWith('strategy:') ? parent.strategy.toLowerCase() === cancelGroupScope.slice(9).toLowerCase() : cancelGroupScope.startsWith('exchange:') ? parent.exchange.toLowerCase() === cancelGroupScope.slice(9).toLowerCase() : false)
  $: cancelScopeCount = cancelGroupScope === 'all' ? workingOrders.length : scopedCancelParents.length
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
	type MarketLinkedRow = NamedRow & { eventId?: string }
	function tickerSelection(event: Event, ticker: string): { quote: PriceQuote; market: MarketView } | null {
		for (const market of event.markets || []) {
			for (const quote of [market.away, market.home, market.over, market.under]) if (quote?.ticker === ticker) return { quote, market }
			for (const option of market.options || []) {
				for (const quote of [option.away, option.home, option.over, option.under]) {
					if (quote?.ticker === ticker) return { quote, market: { ...market, line: option.line, away: option.away, home: option.home, over: option.over, under: option.under } }
				}
			}
		}
		return null
	}
	async function openAccountMarket(row: MarketLinkedRow) {
		const event = snapshot.events.find(candidate => candidate.id === row.eventId && tickerSelection(candidate, row.ticker)) || snapshot.events.find(candidate => tickerSelection(candidate, row.ticker))
		if (!event) return
		const selection = tickerSelection(event, row.ticker)
		if (!selection) return
		navigate('schedule'); query = ''; selectedSport = 'ALL'; selectedLeague = 'ALL'; selectedDate = 'ALL'
		const boardIndex = snapshot.events.filter(candidate => dateKey(candidate.startTime) >= dateKey(new Date()) || candidate.id === event.id).findIndex(candidate => candidate.id === event.id)
		gameDisplayLimit = Math.max(80, boardIndex + 1)
		await select(event, selection.quote, selection.market)
		await tick()
		document.querySelector(`[data-event-id="${CSS.escape(event.id)}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
	}
	function openAccountMarketKey(event: KeyboardEvent, row: MarketLinkedRow) {
		if (event.target !== event.currentTarget) return
		if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openAccountMarket(row) }
	}
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
  const terminalStatus = (status: string) => ['canceled', 'cancelled', 'executed', 'filled', 'closed', 'rejected'].includes((status || '').toLowerCase())
  const rowSearch = (row: NamedRow & { rotation?: string; exchange?: string; status?: string }) => `${rowGame(row)} ${rowDetail(row)} ${row.rotation || ''} ${row.ticker} ${row.exchange || ''} ${row.status || ''}`.toLowerCase()
  const matchesAccount = (row: NamedRow & { rotation?: string; exchange?: string; status?: string }) => !accountQuery || rowSearch(row).includes(accountQuery.toLowerCase())
  const marketMatches = (market?: string) => accountMarket === 'ALL' || (market || '').toUpperCase() === accountMarket
  const fillsForOrder = (order: Order) => snapshot.fills.filter(fill => fill.orderId === order.id || (fill.ticker === order.ticker && fill.side === order.side))
  const fillsForTicker = (ticker: string, side?: string) => snapshot.fills.filter(fill => fill.ticker === ticker && (!side || !fill.side || fill.side === side))
  const parentFills = (parent: ParentOrder) => snapshot.fills.filter(fill => parent.childOrderIds.includes(fill.orderId || ''))
  const toggleAccountRow = (key: string) => expandedAccountRow = expandedAccountRow === key ? '' : key
  function accountRole(row: NamedRow & { side?: string }): SelectionRole {
    const outcome = (row.outcome || '').toLowerCase()
    if (outcome.startsWith('over')) return 'over'
    if (outcome.startsWith('under')) return 'under'
    for (const event of snapshot.events) {
      const selection = tickerSelection(event, row.ticker)
      if (selection) return quoteRole(event, selection.market, selection.quote)
    }
    return row.side === 'no' ? 'home' : 'away'
  }
  function dismissNotice(key: string) { fillNotices = fillNotices.filter(notice => notice.key !== key) }
  function notifyFill(fill: Fill) {
    const key = fill.id || `${fill.ticker}-${Date.now()}`
    fillNotices = [{ key, fill }, ...fillNotices.filter(notice => notice.key !== key)].slice(0, 3)
    unreadFills += 1
    setTimeout(() => dismissNotice(key), 12000)
  }
  function viewFill(key: string) { dismissNotice(key); slipOpen = false; navigate('fills') }
  function openFills() { navigate('fills') }
  async function showHistory(mode: 'settlements' | 'orders' | 'audit') {
    historyMode = mode; navigate('history')
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
	$: currentEvents = snapshot.events.filter(event => dateKey(event.startTime) >= dateKey(new Date()) || event.id === expandedEventID)
	$: dates = ['ALL', ...new Set(currentEvents.map(event => dateKey(event.startTime)))]
  $: filtered = currentEvents.filter(event => {
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
  function toggleGameKey(key: KeyboardEvent, event: Event) {
    if (key.target !== key.currentTarget) return
    if (key.key === 'Enter' || key.key === ' ') { key.preventDefault(); toggleGame(event) }
  }
  const isTypingTarget = (target: globalThis.EventTarget | null) => {
    const element = target instanceof HTMLElement ? target : null
    return Boolean(element && (element.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT', 'BUTTON'].includes(element.tagName)))
  }
  async function focusGame(index: number) {
    if (!filtered.length) return
    if (index >= visibleEvents.length && visibleEvents.length < filtered.length) {
      gameDisplayLimit = Math.min(filtered.length, Math.max(gameDisplayLimit + 80, index + 1))
      await tick()
    }
    if (!visibleEvents.length) return
    keyboardIndex = Math.max(0, Math.min(index, visibleEvents.length - 1))
    await tick()
    const rows = document.querySelectorAll<HTMLElement>('.game-wrap > .game')
    rows[keyboardIndex]?.focus()
    rows[keyboardIndex]?.scrollIntoView({ block: 'nearest' })
  }
  async function handleShortcut(event: KeyboardEvent) {
    if (event.key === '/' && !isTypingTarget(event.target)) {
      event.preventDefault(); view = 'schedule'; await tick(); searchInput?.focus(); searchInput?.select(); return
    }
    if (event.key === 'Escape') {
      if (editingOrderID) stopEditOrder()
      else if (slipOpen) slipOpen = false
      else if (selectedEvent && expandedEventID) toggleGame(selectedEvent)
      return
    }
    if (view !== 'schedule' || isTypingTarget(event.target)) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const activeRow = document.activeElement?.closest('.game-wrap') as HTMLElement | null
      const activeIndex = activeRow ? visibleEvents.findIndex(candidate => candidate.id === activeRow.dataset.eventId) : keyboardIndex
      await focusGame(event.key === 'ArrowDown' ? activeIndex + 1 : Math.max(0, activeIndex - 1))
      return
    }
    if (event.key === 'Enter' && keyboardIndex >= 0 && !document.activeElement?.classList.contains('game')) {
      event.preventDefault(); toggleGame(visibleEvents[keyboardIndex])
    }
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
    if (!bookActionable) return
    slipPrice = level.price
    slipIntent = intent
    slipPolicy = intent === 'cross' ? 'limit' : 'post_only'
    slipCapAuto = true
    slipCap = ''
    slipStatus = ''
    slipOpen = true
  }
  function setBookSide(side: 'yes' | 'no') { bookSide = side; slipOpen = false }
  async function submitOrder() {
	if (!snapshot.health.tradingEnabled || submittingOrder) { slipStatus = submittingOrder ? 'Checking Kalshi—do not resubmit.' : 'Order entry is locked on this server.'; return }
	if (snapshot.health.mode === 'live' && !confirm(`Place a REAL Kalshi order for ${activeOutcome} with ${money(Math.round((Number(slipRisk) || 0) * 10000))} cash at risk?`)) return
    submittingOrder = true
    slipStatus = 'Checking Kalshi—do not resubmit.'
    try {
      const response = await api('/api/parent-orders', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ eventId: selectedEvent?.id, rotation: selectedEvent?.participants.find(participant => participant.name === selectedQuote?.outcome)?.rotation || '', ticker: selectedQuote?.ticker, outcome: activeOutcome, market: marketLabel(selectedMarket), side: bookSide, strategy: slipStrategy, policy: slipPolicy, cashRisk: Math.round((Number(slipRisk) || 0) * 10000), priceCapMoneyline: Number(slipCap), limitPrice: slipPrice, sliceQuantity: Math.round((Number(slipSlice) || 0) * 10000) }) })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Order was rejected')
      slipStatus = `Parent order ${payload.id} created`
    } catch (cause) { slipStatus = cause instanceof Error ? cause.message : 'Unable to submit order' }
    finally { submittingOrder = false }
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
  async function refreshAccount() {
    if (refreshingAccount) return
    refreshingAccount = true
    settingsStatus = 'Refreshing account…'
    try {
      const response = await api('/api/account/refresh', { method: 'POST' })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Unable to refresh account')
      snapshot = normalizeSnapshot(payload as Snapshot)
      settingsStatus = `Account synchronized at ${updated(snapshot.health.accountUpdated)}`
    } catch (cause) {
      settingsStatus = cause instanceof Error ? cause.message : 'Unable to refresh account'
    } finally {
      refreshingAccount = false
    }
  }
  async function cancelOrder(order: Order) {
    if (!snapshot.health.tradingEnabled || cancelingOrderID) return
    if (snapshot.health.mode === 'live' && !confirm(`Cancel this REAL Kalshi order for ${rowGame(order)}?`)) return
    cancelingOrderID = order.id
    try {
      const response = await api(`/api/orders/${encodeURIComponent(order.id)}`, { method: 'DELETE' })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Unable to cancel order')
      snapshot = { ...snapshot, orders: snapshot.orders.map(item => item.id === order.id ? payload as Order : item) }
      cancelGroupStatus = 'Order canceled.'
    } catch (cause) {
      cancelGroupStatus = cause instanceof Error ? cause.message : 'Unable to cancel order'
    } finally {
      cancelingOrderID = ''
    }
  }
  function beginEditOrder(order: Order) {
    editingOrderID = order.id
    editQuantity = `${Math.max(0, order.quantity - order.filledQuantity) / 10000}`
    editLimit = `${order.limitPrice / 100}`
    cancelGroupStatus = ''
  }
  function stopEditOrder() { editingOrderID = ''; savingOrderID = '' }
  function editOrderKey(event: KeyboardEvent, order: Order) {
    event.stopPropagation()
    if (event.key === 'Enter') { event.preventDefault(); void saveOrder(order) }
    if (event.key === 'Escape') { event.preventDefault(); stopEditOrder() }
  }
  async function saveOrder(order: Order) {
    if (!snapshot.health.tradingEnabled || savingOrderID) return
    const remainingQuantity = Math.round(Number(editQuantity) * 10000)
    const limitPrice = Math.round(Number(editLimit) * 100)
    if (remainingQuantity <= 0 || limitPrice <= 0 || limitPrice >= 10000) { cancelGroupStatus = 'Enter a remaining quantity above 0 and a limit from 0.01 to 99.99 cents.'; return }
    if (editOverCap) { cancelGroupStatus = `Estimated all-in risk exceeds the per-order cap of ${money(snapshot.health.maxCashRisk || 0)}.`; return }
    if (editOverAvailable) { cancelGroupStatus = 'This change exceeds available bankroll after releasing the current order reservation.'; return }
    if (snapshot.health.mode === 'live' && !confirm(`Change this REAL Kalshi order to ${editQuantity} remaining contracts at ${editLimit}¢ (${money(editRiskQuote?.cost || 0)} estimated all-in risk)?`)) return
    savingOrderID = order.id
    try {
      const response = await api(`/api/orders/${encodeURIComponent(order.id)}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ remainingQuantity, limitPrice }) })
      const payload = await response.json()
      if (!response.ok) throw new Error(payload.error || 'Unable to edit order')
      snapshot = { ...snapshot, orders: [payload as Order, ...snapshot.orders.filter(item => item.id !== order.id && item.id !== payload.id)] }
      cancelGroupStatus = 'Order updated.'
      stopEditOrder()
    } catch (cause) {
      cancelGroupStatus = cause instanceof Error ? cause.message : 'Unable to edit order'
      savingOrderID = ''
    }
  }
  async function cancelGroup() {
    if (!snapshot.health.tradingEnabled || cancelingGroup) return
    let scope = cancelGroupScope
    let value = ''
    if (cancelGroupScope.includes(':')) [scope, value] = cancelGroupScope.split(':', 2)
    if (scope === 'event') value = selectedEvent?.id || ''
    if (scope === 'event' && !value) { cancelGroupStatus = 'Open a game before canceling its orders.'; return }
    if (scope !== 'all' && snapshot.health.mode === 'live' && !confirm(`Cancel ${cancelScopeCount} REAL managed order${cancelScopeCount === 1 ? '' : 's'} in this scope?`)) return
    cancelingGroup = true
    cancelGroupStatus = 'Canceling…'
    try {
      if (scope === 'all') {
        if (snapshot.health.mode === 'live' && !confirm(`Cancel all ${workingOrders.length} REAL active Kalshi orders?`)) { cancelingGroup = false; cancelGroupStatus = ''; return }
        const response = await api('/api/orders', { method: 'DELETE' })
        const payload = await response.json()
        if (!response.ok && response.status !== 207) throw new Error(payload.error || 'Unable to cancel all active orders')
        const canceledIDs = new Set((payload.canceled || []).map((order: Order) => order.id))
        snapshot = { ...snapshot, orders: snapshot.orders.map(order => canceledIDs.has(order.id) ? { ...order, status: 'canceled', cashRisk: 0 } : order) }
        cancelGroupStatus = payload.failures?.length ? `${payload.canceled.length} canceled · ${payload.failures.length} failed` : `${payload.matched} active order${payload.matched === 1 ? '' : 's'} canceled`
        return
      }
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
    if (streamStopped || streamSocket?.readyState === WebSocket.OPEN || streamSocket?.readyState === WebSocket.CONNECTING) return
    browserStreamState = reconnectDelay > 1000 ? 'reconnecting' : 'connecting'
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${location.host}/api/ws`)
    streamSocket = socket
    socket.onopen = () => { browserStreamState = 'live'; reconnectDelay = 1000 }
    socket.onmessage = event => {
      try { applyStream(JSON.parse(event.data)) }
      catch { /* ignore one malformed browser event and keep the stream alive */ }
    }
    socket.onerror = () => socket.close()
    socket.onclose = () => {
      if (streamSocket === socket) streamSocket = null
      if (streamStopped) return
      browserStreamState = 'reconnecting'
      if (book) book = { ...book, stale: true }
      if (reconnectTimer) clearTimeout(reconnectTimer)
      reconnectTimer = setTimeout(connect, reconnectDelay)
      reconnectDelay = Math.min(reconnectDelay * 2, 10000)
    }
  }
  async function loadInitialSnapshot() {
    try {
      const response = await api('/api/snapshot')
      if (!response.ok) throw new Error(`Server returned ${response.status}`)
      if (streamStopped) return
      snapshot = normalizeSnapshot(await response.json())
      snapshot.fills.forEach(fill => seenFillIDs.add(fill.id))
      draftSports = snapshot.settings.availableSports.filter(option => option.enabled).map(option => option.name)
      draftExcludeAddedGames = snapshot.settings.preferences.excludeAddedGames
      error = ''
      snapshotRetryDelay = 1000
      connect()
    } catch (cause) {
      if (streamStopped) return
      error = `${cause instanceof Error ? cause.message : 'Unable to load PMBattle'}. Retrying…`
      browserStreamState = 'reconnecting'
      if (snapshotRetryTimer) clearTimeout(snapshotRetryTimer)
      snapshotRetryTimer = setTimeout(loadInitialSnapshot, snapshotRetryDelay)
      snapshotRetryDelay = Math.min(snapshotRetryDelay * 2, 10000)
    }
  }
  onMount(() => {
    document.documentElement.dataset.theme = theme
    view = pathView(window.location.pathname)
    const popstate = () => { view = pathView(window.location.pathname); if (view === 'fills') unreadFills = 0 }
    window.addEventListener('popstate', popstate)
    streamStopped = false
    void loadInitialSnapshot()
    return () => {
      streamStopped = true
      if (reconnectTimer) clearTimeout(reconnectTimer)
      if (snapshotRetryTimer) clearTimeout(snapshotRetryTimer)
      reconnectTimer = null
      snapshotRetryTimer = null
      streamSocket?.close()
      streamSocket = null
      window.removeEventListener('popstate', popstate)
    }
  })
  $: orderStrategies = ['ALL', ...new Set(snapshot.parentOrders.map(parent => parent.strategy.toUpperCase()).filter(Boolean))]
  $: filteredOrders = snapshot.orders.filter(order => matchesAccount(order) && marketMatches(order.market) && (accountStrategy === 'ALL' || (parentForOrder(order)?.strategy || 'basic').toUpperCase() === accountStrategy) && (accountStatus === 'all' || accountStatus === 'active' && !terminalStatus(order.status) || accountStatus === 'partial' && order.filledQuantity > 0 && !terminalStatus(order.status) || accountStatus === 'completed' && ['filled', 'executed', 'closed'].includes(order.status.toLowerCase()) || accountStatus === 'canceled' && ['canceled', 'cancelled', 'rejected'].includes(order.status.toLowerCase())))
  $: filteredPositions = snapshot.positions.filter(position => matchesAccount(position) && marketMatches(position.market))
  $: filteredFills = snapshot.fills.filter(fill => matchesAccount(fill) && marketMatches(fill.market))
  $: completedParents = snapshot.parentOrders.filter(parent => terminalStatus(parent.status) && matchesAccount(parent) && marketMatches(parent.market) && (accountStrategy === 'ALL' || parent.strategy.toUpperCase() === accountStrategy))
  $: recentFills = filteredFills.slice(0, accountDisplayLimit)
  $: fillsToday = snapshot.fills.filter(fill => dateKey(fill.createdAt) === dateKey(new Date()))
  $: settlementsToday = snapshot.settlements.filter(settlement => dateKey(settlement.settledAt) === dateKey(new Date()))
  $: workingRisk = workingOrders.reduce((total, order) => total + Math.max(0, order.cashRisk), 0)
  $: positionRisk = snapshot.positions.reduce((total, position) => total + Math.abs(position.cashRisk), 0)
  $: positionFees = snapshot.positions.reduce((total, position) => total + (position.feesPaid || 0), 0)
  $: realizedPnL = snapshot.positions.reduce((total, position) => total + (position.realizedPnl || 0), 0)
  $: todayFillRisk = fillsToday.reduce((total, fill) => total + fill.cashRisk, 0)
  $: todayFillFees = fillsToday.reduce((total, fill) => total + fill.fee, 0)
  $: todayFillQuantity = fillsToday.reduce((total, fill) => total + fill.quantity, 0)
  $: todaySettledPnL = settlementsToday.reduce((total, settlement) => total + settlement.netPnl, 0)
  $: visibleEvents = filtered.slice(0, gameDisplayLimit)

  function resetGameBatch() { gameDisplayLimit = 80; keyboardIndex = -1 }
  onMount(() => {
    window.addEventListener('keydown', handleShortcut)
    return () => window.removeEventListener('keydown', handleShortcut)
  })
  $: document.documentElement.dataset.theme = theme
</script>

<svelte:head><title>PMBattle</title><meta name="description" content="Fast sportsbook-style prediction market terminal"></svelte:head>

<div class="app-shell">
  <header class="topbar">
    <strong class="brand">PMBATTLE</strong>
    <nav class="primary-nav" aria-label="Application"><button class:active={view === 'schedule'} on:click={() => navigate('schedule')}>Schedule</button><button class:active={view === 'orders'} on:click={() => navigate('orders')}>Orders</button><button class:active={view === 'positions'} on:click={() => navigate('positions')}>Positions</button><button class:active={view === 'fills'} on:click={() => navigate('fills')}>Fills</button><button class:active={view === 'history'} on:click={() => navigate('history')}>History</button><button class:active={view === 'settings'} on:click={() => navigate('settings')}>Settings</button></nav>
    {#if view === 'schedule'}<label class="search"><span aria-hidden="true">⌕</span><input bind:this={searchInput} bind:value={query} on:input={resetGameBatch} aria-label="Search games" placeholder="Search game # or team" /></label>{:else if view !== 'settings'}<label class="search"><span aria-hidden="true">⌕</span><input bind:value={accountQuery} on:input={() => accountDisplayLimit = 100} aria-label={`Search ${view}`} placeholder="Search team, game #, market or ticker" /></label>{/if}
    <div class="health" class:is-stale={snapshot.health.status !== 'ok' || browserStreamState !== 'live'} title={`Browser stream: ${browserStreamState}`}><i></i><span>{snapshot.health.mode.toUpperCase()} · {snapshot.health.exchangeState.toUpperCase()} · UI {browserStreamState.toUpperCase()}</span></div>
    <div class="theme"><button class:active={theme === 'light'} on:click={() => setTheme('light')}>Light</button><button class:active={theme === 'dark'} on:click={() => setTheme('dark')}>Dark</button></div>
  </header>
  {#if view === 'schedule'}
  <nav class="sports" aria-label="Sport filters">
    {#each sports as sport}<button class:active={selectedSport === sport} on:click={() => { selectedSport = sport; selectedLeague = 'ALL'; resetGameBatch() }}>{sport}</button>{/each}
    <span class="account">Available <b>{money(snapshot.bankroll)}</b> · New orders <b>{money(snapshot.availableToAllocate)}</b> · At risk <b>{money(snapshot.atRisk)}</b></span>
  </nav>
  <div class="filters">
    <select bind:value={selectedDate} on:change={resetGameBatch} aria-label="Date"><option value="ALL">All dates</option>{#each dates.slice(1) as date}<option value={date}>{new Date(`${date}T12:00:00`).toLocaleDateString()}</option>{/each}</select>
    <select bind:value={selectedLeague} on:change={resetGameBatch} aria-label="League"><option value="ALL">All leagues</option>{#each leagues.slice(1) as league}<option>{league}</option>{/each}</select>
    <span>{visibleEvents.length === filtered.length ? `${filtered.length} games` : `${visibleEvents.length} of ${filtered.length} games`}</span>
    <small class="shortcut-help">/ search · ↑↓ games · Enter open · Esc close</small>
  </div>

  {#if error}<div class="error" role="alert">{error}</div>{/if}
  <div class="workspace">
    <main class="board">
      <div class="board-head"><span>Game</span><span>Team</span><span>Moneyline</span><span>Spread</span><span>Total</span><span>Time</span></div>
      {#each visibleEvents as event (event.id)}
        {@const moneyline = event.markets?.find(market => market.type === 'moneyline')}
        {@const spreadMarket = event.markets?.find(market => market.type === 'spread')}
        {@const total = event.markets?.find(market => market.type === 'total')}
		<div class="game-wrap" class:expanded={expandedEventID === event.id} data-event-id={event.id}>
        <section class="game" class:selected={selectedEvent?.id === event.id} role="button" tabindex="0" on:click={() => toggleGame(event)} on:keydown={(key) => toggleGameKey(key, event)}>
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
                  <button class="ladder-row" disabled={!bookActionable} title={bookActionable ? 'Use this ask in the order slip' : 'Waiting for a synchronized live book'} on:click={() => chooseBookPrice(level, 'cross')} style={`--depth:${Math.min(100, Number(level.quantity) / Math.max(1, ...displayAsks.map(value => Number(value.quantity))) * 100)}%`}><b>ASK</b><span>{levelPrice(level)}</span><span>{qty(level.quantity)}</span><span>{money(takerQuote(level).cost)}</span></button>
                {/each}
              </div>
              <div class="book-center" class:role-away={activeRole === 'away'} class:role-home={activeRole === 'home'} class:role-over={activeRole === 'over'} class:role-under={activeRole === 'under'}><b>{activeRole.toUpperCase()} · Trade {bookSide === 'yes' ? 'Yes' : 'No'}</b><span>{activeOutcome}</span></div>
              <div class="ladder bids">
                {#each displayBids as level}
                  {@const own = ownOrdersAtLevel(level)}
                  <button class="ladder-row" class:own-level={own.quantity > 0} disabled={!bookActionable} title={own.quantity > 0 ? `Your ${qty(own.quantity)} remaining contracts are resting at this price` : bookActionable ? 'Join this bid in the order slip' : 'Waiting for a synchronized live book'} on:click={() => chooseBookPrice(level, 'join')} style={`--depth:${Math.min(100, Number(level.quantity) / Math.max(1, ...displayBids.map(value => Number(value.quantity))) * 100)}%`}><b>BID{#if own.quantity > 0}<i>YOU · {qty(own.quantity)}</i>{/if}</b><span>{levelPrice(level, true)}</span><span>{qty(level.quantity)}</span><span>{money(makerQuote(level).cost)}</span></button>
                {/each}
              </div>
            {:else}
              <div class="book-wait"><b>Opening live order book…</b><span>Only this selected market is being loaded.</span></div>
            {/if}
            <footer class="book-footer"><span>Kalshi · {selectedQuote.ticker}{#if book?.updatedAt} · updated {ago(book.updatedAt)} · seq {book.sequence}{/if}</span><span>Maker estimate {money(selectedQuote.makerFee)} · Taker estimate {money(selectedQuote.takerFee)}</span></footer>
          </section>
        {/if}
        </div>
      {:else}<div class="empty">No matching games</div>{/each}
      {#if visibleEvents.length < filtered.length}<div class="board-more"><button on:click={() => gameDisplayLimit += 80}>Show 80 more games</button><span>{filtered.length - visibleEvents.length} remaining</span></div>{/if}
    </main>
  </div>

  {:else if view === 'orders'}
    <main class="account-page">
      <header class="account-page-head"><div><small>ACCOUNT WORKSPACE</small><h1>Orders</h1><p>Manage active orders, inspect strategy children, and confirm every exchange action.</p></div><button disabled={refreshingAccount} on:click={refreshAccount}>{refreshingAccount ? 'Refreshing…' : 'Refresh account'}</button></header>
      <section class="account-summary"><span><small>Working</small><b>{workingOrders.length}</b></span><span><small>Partial</small><b>{workingOrders.filter(order => order.filledQuantity > 0).length}</b></span><span><small>Reserved</small><b>{money(workingRisk)}</b></span><span><small>Filled today</small><b>{qty(todayFillQuantity)}</b></span><span><small>Available</small><b>{money(snapshot.availableToAllocate)}</b></span><span><small>Account sync</small><b>{updated(snapshot.health.accountUpdated)}</b></span></section>
      <section class="account-toolbar"><select bind:value={accountStatus} aria-label="Order status"><option value="active">Active orders</option><option value="partial">Partially filled</option><option value="completed">Filled</option><option value="canceled">Canceled / rejected</option><option value="all">All current records</option></select><select bind:value={accountMarket} aria-label="Market type"><option>ALL</option><option>MONEYLINE</option><option>SPREAD</option><option>TOTAL</option></select><select bind:value={accountStrategy} aria-label="Strategy"><option>ALL</option>{#each orderStrategies.slice(1) as strategy}<option>{strategy}</option>{/each}</select><span>{filteredOrders.length} order{filteredOrders.length === 1 ? '' : 's'}</span></section>
      {#if snapshot.health.tradingEnabled}<div class="cancel-scope-bar account-kill"><b>{snapshot.health.mode === 'live' ? 'Real-order controls' : 'Demo controls'}</b><select bind:value={cancelGroupScope} aria-label="Cancel scope"><option value="all">All active Kalshi orders</option><option value="strategy:basic">Basic orders</option><option value="strategy:iceberg">Iceberg orders</option><option value="strategy:follow">Follow orders</option><option value="exchange:Kalshi">Kalshi managed orders</option></select><button disabled={cancelingGroup || cancelScopeCount === 0} on:click={cancelGroup}>{cancelingGroup ? 'Canceling…' : `Cancel ${cancelScopeCount}`}</button><small aria-live="polite">{cancelGroupStatus}</small></div>{/if}
      <section class="account-list">
        <div class="account-list-head order-grid"><span>Game / bet</span><span>Status</span><span>Strategy</span><span class="num">Filled / total</span><span class="num">Limit</span><span class="num">Remaining risk</span><span>Actions</span></div>
        {#each filteredOrders.slice(0, accountDisplayLimit) as order (order.id)}
          {@const parent = parentForOrder(order)}{@const role = accountRole(order)}{@const orderFills = fillsForOrder(order)}
          <article class="account-record" class:expanded={expandedAccountRow === `order:${order.id}`}>
            <div class="account-row order-grid" role="button" tabindex="0" on:click={() => toggleAccountRow(`order:${order.id}`)} on:keydown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggleAccountRow(`order:${order.id}`) } }}><span class={`account-name role-${role}`}><i>{role.toUpperCase()}</i><b>{rowGame(order)}</b><small>{rowDetail(order)} · {ago(order.createdAt)}</small></span><span><i class="pill {orderStatus(order).tone}">{orderStatus(order).label}</i><small>{order.exchange}</small></span><span><b>{parent?.strategy || 'basic'}</b><small>{parent?.policy || 'exchange order'}</small></span><span class="num"><b>{qty(order.filledQuantity)} / {qty(order.quantity)}</b><small>{qty(Math.max(0, order.quantity - order.filledQuantity))} remaining</small></span><span class="num"><b>{orderOdds(order)}</b><small>{order.limitPrice / 100}¢ raw → fee included</small></span><span class="num"><b>{money(order.cashRisk)}</b><small>{parent ? `${money(parent.remainingRisk)} parent` : 'order reservation'}</small></span><span class="row-actions"><button on:click|stopPropagation={() => openAccountMarket(order)}>Market</button>{#if snapshot.health.tradingEnabled && workingOrders.includes(order)}<button on:click|stopPropagation={() => beginEditOrder(order)}>Edit</button><button class="danger" disabled={cancelingOrderID === order.id} on:click|stopPropagation={() => cancelOrder(order)}>{cancelingOrderID === order.id ? 'Canceling…' : 'Cancel'}</button>{/if}</span></div>
            {#if expandedAccountRow === `order:${order.id}`}<div class="account-detail">
              <div class="detail-metrics"><span><small>Order ID</small><b title={order.id}>{order.id}</b></span><span><small>Ticker</small><b>{order.ticker}</b></span><span><small>Created</small><b>{new Date(order.createdAt).toLocaleString()}</b></span><span><small>Fills</small><b>{orderFills.length}</b></span>{#if parent}<span><small>Parent target</small><b>{money(parent.cashRiskTarget)}</b></span><span><small>Price cap</small><b>{ml(parent.priceCapMoneyline)}</b></span><span><small>Children</small><b>{parent.childOrderIds.length}</b></span><span><small>Reprices</small><b>{parent.replaceCount || 0}</b></span>{/if}</div>
              {#if editingOrderID === order.id}<div class="inline-edit"><label>Remaining contracts<input type="number" min="0.01" step="0.01" bind:value={editQuantity} on:keydown={(event) => editOrderKey(event, order)} /></label><label>Limit cents<input type="number" min="0.01" max="99.99" step="0.01" bind:value={editLimit} on:keydown={(event) => editOrderKey(event, order)} /></label><span><small>Conservative all-in risk</small><b class:negative={editOverCap || editOverAvailable}>{money(editRiskQuote?.cost || 0)}</b></span><button disabled={Boolean(savingOrderID) || editOverCap || editOverAvailable} on:click={() => saveOrder(order)}>{savingOrderID ? 'Saving…' : 'Save change'}</button><button on:click={stopEditOrder}>Close</button></div>{/if}
              {#if parent?.children?.length}<div class="mini-table"><b>Strategy children</b>{#each parent.children as child}<span>{child.id.slice(0, 12)}…</span><span>{child.status}</span><span class="num">{qty(child.filledQuantity)} / {qty(child.quantity)}</span><span>{updated(child.updatedAt)}</span>{/each}</div>{/if}
              {#if orderFills.length}<div class="mini-table"><b>Related fills</b>{#each orderFills.slice(0, 8) as fill}<span>{new Date(fill.createdAt).toLocaleString()}</span><span>{qty(fill.quantity)} contracts</span><span class="num">{ml(fill.allInMoneyline)}</span><span class="num">{money(fill.fee)} fee</span>{/each}</div>{:else}<p class="detail-empty">No fills recorded for this order.</p>{/if}
            </div>{/if}
          </article>
        {:else}<div class="empty">No orders match these filters.</div>{/each}
      </section>
    </main>
  {:else if view === 'positions'}
    <main class="account-page">
      <header class="account-page-head"><div><small>ACCOUNT WORKSPACE</small><h1>Positions</h1><p>Current exposure, fee-included entry odds, and the fills behind every position.</p></div><button disabled={refreshingAccount} on:click={refreshAccount}>{refreshingAccount ? 'Refreshing…' : 'Refresh account'}</button></header>
      <section class="account-summary"><span><small>Open positions</small><b>{snapshot.positions.length}</b></span><span><small>Cash at risk</small><b>{money(positionRisk)}</b></span><span><small>Fees paid</small><b>{money(positionFees)}</b></span><span><small>Realized P&amp;L</small><b class:positive={realizedPnL >= 0} class:negative={realizedPnL < 0}>{money(realizedPnL)}</b></span><span><small>Available</small><b>{money(snapshot.availableToAllocate)}</b></span><span><small>Account sync</small><b>{updated(snapshot.health.accountUpdated)}</b></span></section>
      <section class="account-toolbar"><select bind:value={accountMarket} aria-label="Market type"><option>ALL</option><option>MONEYLINE</option><option>SPREAD</option><option>TOTAL</option></select><span>{filteredPositions.length} position{filteredPositions.length === 1 ? '' : 's'}</span></section>
      <section class="account-list"><div class="account-list-head position-grid"><span>Game / bet</span><span>Side</span><span class="num">Contracts</span><span class="num">Average odds</span><span class="num">Cash at risk</span><span class="num">Fees</span><span class="num">P&amp;L</span></div>
        {#each filteredPositions.slice(0, accountDisplayLimit) as position (`${position.ticker}:${position.side}`)}{@const role = accountRole(position)}{@const relatedFills = fillsForTicker(position.ticker, position.side)}
          <article class="account-record" class:expanded={expandedAccountRow === `position:${position.ticker}:${position.side}`}><div class="account-row position-grid" role="button" tabindex="0" on:click={() => toggleAccountRow(`position:${position.ticker}:${position.side}`)} on:keydown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggleAccountRow(`position:${position.ticker}:${position.side}`) } }}><span class={`account-name role-${role}`}><i>{role.toUpperCase()}</i><b>{rowGame(position)}</b><small>{rowDetail(position)}</small></span><span><b>{(position.side || '—').toUpperCase()}</b><small>{position.exchange}</small></span><span class="num"><b>{qty(Math.abs(position.quantity))}</b></span><span class="num"><b>{positionOdds(position)}</b><small>raw → fee included</small></span><span class="num"><b>{money(position.cashRisk)}</b><small>{money(position.totalTraded || 0)} traded</small></span><span class="num">{money(position.feesPaid || 0)}</span><span class="num" class:positive={(position.realizedPnl || 0) >= 0} class:negative={(position.realizedPnl || 0) < 0}><b>{money(position.realizedPnl || 0)}</b><button class="market-link" on:click|stopPropagation={() => openAccountMarket(position)}>Open market</button></span></div>
          {#if expandedAccountRow === `position:${position.ticker}:${position.side}`}<div class="account-detail"><div class="detail-metrics"><span><small>Ticker</small><b>{position.ticker}</b></span><span><small>Average raw</small><b>{position.averagePrice ? `${position.averagePrice / 100}¢` : '—'}</b></span><span><small>Current value</small><b>{position.currentPrice ? `${position.currentPrice / 100}¢` : 'Book required'}</b></span><span><small>Last update</small><b>{updated(position.updatedAt)}</b></span></div>{#if relatedFills.length}<div class="mini-table"><b>Position fills</b>{#each relatedFills.slice(0, 12) as fill}<span>{new Date(fill.createdAt).toLocaleString()}</span><span>{qty(fill.quantity)} contracts</span><span class="num">{ml(fill.allInMoneyline)}</span><span class="num">{money(fill.cashRisk)}</span>{/each}</div>{:else}<p class="detail-empty">No recovered fills are linked to this position yet.</p>{/if}</div>{/if}</article>
        {:else}<div class="empty">No positions match these filters.</div>{/each}
      </section>
    </main>
  {:else if view === 'fills'}
    <main class="account-page">
      <header class="account-page-head"><div><small>ACCOUNT WORKSPACE</small><h1>Fills</h1><p>Every full and partial execution, including fills recovered while PMBattle was offline.</p></div><button disabled={refreshingAccount} on:click={refreshAccount}>{refreshingAccount ? 'Refreshing…' : 'Refresh account'}</button></header>
      <section class="account-summary"><span><small>Fills today</small><b>{fillsToday.length}</b></span><span><small>Contracts today</small><b>{qty(todayFillQuantity)}</b></span><span><small>Cash filled</small><b>{money(todayFillRisk)}</b></span><span><small>Fees today</small><b>{money(todayFillFees)}</b></span><span><small>Latest fill</small><b>{snapshot.fills[0] ? time(snapshot.fills[0].createdAt) : '—'}</b></span><span><small>Account sync</small><b>{updated(snapshot.health.accountUpdated)}</b></span></section>
      <section class="account-toolbar"><select bind:value={accountMarket} aria-label="Market type"><option>ALL</option><option>MONEYLINE</option><option>SPREAD</option><option>TOTAL</option></select><span>{filteredFills.length} fill{filteredFills.length === 1 ? '' : 's'}</span></section>
      <section class="account-list"><div class="account-list-head fill-grid"><span>Time / game</span><span>Bet</span><span class="num">Quantity</span><span class="num">Raw</span><span class="num">All-in odds</span><span class="num">Fee</span><span class="num">Cash risk</span></div>
        {#each recentFills as fill (fill.id)}{@const role = accountRole({ ...fill, outcome: fill.team })}
          <article class="account-record" class:expanded={expandedAccountRow === `fill:${fill.id}`}><div class="account-row fill-grid" role="button" tabindex="0" on:click={() => toggleAccountRow(`fill:${fill.id}`)} on:keydown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggleAccountRow(`fill:${fill.id}`) } }}><span><b>{new Date(fill.createdAt).toLocaleString()}</b><small>{rowGame(fill)}</small></span><span class={`account-name role-${role}`}><i>{role.toUpperCase()}</i><b>{fillName(fill)}</b><small>{fill.exchange}</small></span><span class="num">{qty(fill.quantity)}</span><span class="num">{fill.rawPrice / 100}¢</span><span class="num"><b>{ml(fill.allInMoneyline)}</b></span><span class="num">{money(fill.fee)}</span><span class="num"><b>{money(fill.cashRisk)}</b><button class="market-link" on:click|stopPropagation={() => openAccountMarket(fill)}>Open market</button></span></div>
          {#if expandedAccountRow === `fill:${fill.id}`}<div class="account-detail"><div class="detail-metrics"><span><small>Fill ID</small><b>{fill.id}</b></span><span><small>Order ID</small><b>{fill.orderId || 'Not reported'}</b></span><span><small>Ticker</small><b>{fill.ticker}</b></span><span><small>Side</small><b>{(fill.side || '—').toUpperCase()}</b></span></div></div>{/if}</article>
        {:else}<div class="empty">No fills match these filters.</div>{/each}
        {#if recentFills.length < filteredFills.length}<div class="account-more"><button on:click={() => accountDisplayLimit += 100}>Show 100 earlier fills</button><span>{filteredFills.length - recentFills.length} remaining</span></div>{/if}
      </section>
    </main>
  {:else if view === 'history'}
    <main class="account-page">
      <header class="account-page-head"><div><small>ACCOUNT WORKSPACE</small><h1>History</h1><p>Settlements, completed strategies, and the immutable system audit trail.</p></div><button disabled={refreshingAccount} on:click={refreshAccount}>{refreshingAccount ? 'Refreshing…' : 'Refresh account'}</button></header>
      <section class="account-summary"><span><small>Settlements</small><b>{snapshot.settlements.length}</b></span><span><small>Settled today</small><b>{settlementsToday.length}</b></span><span><small>Today P&amp;L</small><b class:positive={todaySettledPnL >= 0} class:negative={todaySettledPnL < 0}>{money(todaySettledPnL)}</b></span><span><small>Completed parents</small><b>{snapshot.parentOrders.filter(parent => terminalStatus(parent.status)).length}</b></span><span><small>Audit loaded</small><b>{auditRecords.length}</b></span><span><small>Account sync</small><b>{updated(snapshot.health.accountUpdated)}</b></span></section>
      <div class="history-switch page-switch"><button class:active={historyMode === 'settlements'} on:click={() => showHistory('settlements')}>Settlements</button><button class:active={historyMode === 'orders'} on:click={() => showHistory('orders')}>Completed orders</button><button class:active={historyMode === 'audit'} on:click={() => showHistory('audit')}>System audit</button><span>{historyMode === 'audit' ? 'Loaded only when requested' : 'Recovered account history'}</span></div>
      {#if historyMode === 'settlements'}<section class="account-list"><div class="account-list-head settlement-grid"><span>Settled / game</span><span>Bet</span><span>Result</span><span class="num">Yes / No</span><span class="num">Revenue</span><span class="num">Fees</span><span class="num">Net P&amp;L</span></div>{#each snapshot.settlements.filter(settlement => matchesAccount(settlement) && marketMatches(settlement.market)).slice(0, accountDisplayLimit) as settlement (`${settlement.ticker}:${settlement.settledAt}`)}{@const role = accountRole(settlement)}<article class="account-record"><div class="account-row settlement-grid"><span><b>{new Date(settlement.settledAt).toLocaleString()}</b><small>{rowGame(settlement)}</small></span><span class={`account-name role-${role}`}><i>{role.toUpperCase()}</i><b>{rowDetail(settlement)}</b><button class="market-link" on:click={() => openAccountMarket(settlement)}>Open market</button></span><span><b>{settlement.result.toUpperCase()}</b></span><span class="num">{qty(settlement.yesQuantity)} / {qty(settlement.noQuantity)}</span><span class="num">{money(settlement.revenue)}</span><span class="num">{money(settlement.fee)}</span><span class="num" class:positive={settlement.netPnl >= 0} class:negative={settlement.netPnl < 0}><b>{money(settlement.netPnl)}</b></span></div></article>{:else}<div class="empty">No settlements match these filters.</div>{/each}</section>
      {:else if historyMode === 'orders'}<section class="account-list"><div class="account-list-head completed-grid"><span>Game / bet</span><span>Status</span><span>Strategy</span><span class="num">Filled</span><span class="num">Target</span><span class="num">Remaining</span><span>Completed</span></div>{#each completedParents.slice(0, accountDisplayLimit) as parent (parent.id)}{@const role = accountRole(parent)}{@const fills = parentFills(parent)}<article class="account-record" class:expanded={expandedAccountRow === `parent:${parent.id}`}><div class="account-row completed-grid" role="button" tabindex="0" on:click={() => toggleAccountRow(`parent:${parent.id}`)}><span class={`account-name role-${role}`}><i>{role.toUpperCase()}</i><b>{parent.outcome}</b><small>{parent.rotation ? `#${parent.rotation} · ` : ''}{parent.market}</small></span><span><i class="pill {statusLabels[parent.status]?.tone || 'off'}">{statusLabels[parent.status]?.label || parent.status}</i></span><span>{parent.strategy}<small>{parent.policy}</small></span><span class="num">{money(parent.filledRisk)}</span><span class="num">{money(parent.cashRiskTarget)}</span><span class="num">{money(parent.remainingRisk)}</span><span>{new Date(parent.updatedAt).toLocaleString()}<small>{fills.length} fills · {parent.childOrderIds.length} children</small></span></div>{#if expandedAccountRow === `parent:${parent.id}`}<div class="account-detail"><div class="detail-metrics"><span><small>Parent ID</small><b>{parent.id}</b></span><span><small>Ticker</small><b>{parent.ticker}</b></span><span><small>Price cap</small><b>{ml(parent.priceCapMoneyline)}</b></span><span><small>Reprices</small><b>{parent.replaceCount || 0}</b></span></div></div>{/if}</article>{:else}<div class="empty">No completed orders match these filters.</div>{/each}</section>
      {:else}<section class="account-list"><div class="audit-list">{#each auditRecords as record (record.id)}<article><span><b>{new Date(record.occurredAt).toLocaleString()}</b><small>Record #{record.id}</small></span><span><b>{auditTitle(record.kind)}</b><small>{auditSummary(record)}</small></span><details><summary>Details</summary><pre>{JSON.stringify(record.payload, null, 2)}</pre></details></article>{:else}{#if !auditLoading}<div class="empty">No order activity has been audited yet.</div>{/if}{/each}</div>{#if auditError}<div class="error" role="alert">{auditError}</div>{/if}{#if auditLoading}<div class="audit-more">Loading audit history…</div>{:else if auditHasMore}<div class="audit-more"><button on:click={() => loadAudit(false)}>Load earlier records</button></div>{/if}</section>{/if}
    </main>
  {:else}
    <main class="settings-page">
      <header><h1>Settings</h1><p>Choose the sports PMBattle should load and subscribe to. Your choices are saved on this server.</p></header>
      <section class="settings-section safety-section">
		<div class="settings-heading"><div><h2>Trading status</h2><p>Order entry is controlled when the server starts.</p></div><div class="settings-actions"><button disabled={refreshingAccount} on:click={refreshAccount}>{refreshingAccount ? 'Refreshing…' : 'Refresh account'}</button><span class="safety-badge" class:armed={snapshot.health.tradingEnabled}>{snapshot.health.tradingEnabled ? snapshot.health.mode === 'live' ? 'LIVE TRADING' : 'DEMO TRADING' : 'READ-ONLY'}</span></div></div>
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
  <section class="account-monitor" aria-label="Live account monitor">
    <button class:active={view === 'positions'} on:click={() => navigate('positions')}><small>POSITIONS</small><b>{snapshot.positions.length}</b><span>{money(positionRisk)} risk</span></button>
    <button class:active={view === 'orders'} on:click={() => navigate('orders')}><small>ORDERS</small><b>{workingOrders.length}</b><span>{money(workingRisk)} reserved</span></button>
    <button class:active={view === 'fills'} on:click={openFills}><small>FILLS{unreadFills ? ` · ${unreadFills} NEW` : ''}</small><b>{snapshot.fills.length}</b><span>{snapshot.fills[0] ? `${fillName(snapshot.fills[0])} · ${ago(snapshot.fills[0].createdAt)}` : 'Monitoring live fills'}</span></button>
    <button class:active={view === 'history'} on:click={() => showHistory('settlements')}><small>HISTORY</small><b>{snapshot.settlements.length}</b><span>{settlementsToday.length} settled today</span></button>
    <span class="monitor-feed" class:stale={snapshot.health.status !== 'ok' || browserStreamState !== 'live'}><i></i><b>{browserStreamState === 'live' ? 'LIVE' : browserStreamState.toUpperCase()}</b><small>{updated(snapshot.health.accountUpdated)}</small></span>
  </section>
  <section class="fill-notices" aria-live="assertive" aria-label="Fill notifications">
    {#each fillNotices as notice (notice.key)}
      <article class="fill-notice"><i></i><div><small>FILL RECEIVED</small><b>{fillName(notice.fill)}</b><span>{rowGame(notice.fill)} · {qty(notice.fill.quantity)} @ {ml(notice.fill.allInMoneyline)}</span></div><button on:click={() => viewFill(notice.key)}>View</button><button class="notice-close" aria-label="Dismiss fill notification" on:click={() => dismissNotice(notice.key)}>×</button></article>
    {/each}
  </section>
  {#if slipOpen && selectedQuote && selectedEvent}
    <aside class="order-slip" aria-label="Order slip">
      <header class:role-away={activeRole === 'away'} class:role-home={activeRole === 'home'} class:role-over={activeRole === 'over'} class:role-under={activeRole === 'under'}><div><small>ORDER SLIP · KALSHI · <i class="selection-role">{activeRole.toUpperCase()}</i></small><b>{activeOutcome} {selectedMarket?.line || ''}</b></div><button aria-label="Close order slip" on:click={() => slipOpen = false}>×</button></header>
      <div class="slip-price"><span>{slipIntent === 'cross' ? `Buy ${bookSide.toUpperCase()}` : `Join ${bookSide.toUpperCase()} bid`}</span><b>{ml(rawML(slipPrice))} <i>→ {ml(slipQuote?.moneyline)}</i></b><small>raw → conservative taker</small></div>
      <div class="slip-strategies"><button class:active={slipStrategy === 'basic'} on:click={() => slipStrategy = 'basic'}>Basic</button><button class:active={slipStrategy === 'iceberg'} on:click={() => { slipStrategy = 'iceberg'; if (slipPolicy === 'ioc') slipPolicy = 'limit' }}>Iceberg</button><button class:active={slipStrategy === 'follow'} on:click={() => { slipStrategy = 'follow'; slipPolicy = 'post_only' }}>Follow</button></div>
      <div class="slip-fields">
        <label><span>Cash at risk</span><div><i>$</i><input type="number" min="1" step="1" bind:value={slipRisk} /></div></label>
        <label><span>Worst all-in odds</span><input type="number" step="1" bind:value={slipCap} on:input={() => slipCapAuto = false} /></label>
        <label><span>Order behavior</span><select bind:value={slipPolicy} disabled={slipStrategy === 'follow'}><option value="limit">Limit</option><option value="post_only">Post only</option><option value="ioc" disabled={slipStrategy !== 'basic'}>Immediate or cancel</option></select></label>
        {#if slipStrategy === 'iceberg'}<label><span>Visible contracts</span><input type="number" min="1" step="1" bind:value={slipSlice} /></label>{/if}
      </div>
      <div class="slip-summary"><span>Contracts <b>{qty(slipQuantity)}</b></span><span>Maker <b>{ml(slipMakerQuote?.moneyline)} · {money(slipMakerQuote?.fee || 0)} fee</b></span><span>Taker <b>{ml(slipQuote?.moneyline)} · {money(slipQuote?.fee || 0)} fee</b></span><span>Reserved <b>{money(slipQuote?.cost || 0)}</b></span></div>
      {#if slipOverCap}<p class="slip-status" role="alert">Cash at risk is above this server's per-order cap of {money(snapshot.health.maxCashRisk || 0)}.</p>{/if}
      {#if slipCapInvalid}<p class="slip-status" role="alert">Enter American odds of +100 or higher, or -100 or lower.</p>{:else if slipBeyondCap}<p class="slip-status" role="alert">The fee-included price is worse than your limit.</p>{/if}
      {#if slipInvalidSlice}<p class="slip-status" role="alert">Visible iceberg contracts must be above zero and smaller than the estimated total of {qty(slipQuantity)}.</p>{/if}
      {#if !bookActionable}<p class="slip-status" role="alert">Live book synchronization was lost. Review is disabled until a fresh snapshot arrives.</p>{/if}
      {#if slipStrategy === 'follow'}<p class="slip-status">Joins the live top bid, stays post-only, and pauses at your all-in cap or on stale data.</p>{/if}
	  <button class="submit-order" disabled={submittingOrder || !snapshot.health.tradingEnabled || !bookActionable || !slipPrice || !slipQuantity || Number(slipRisk) <= 0 || slipOverCap || slipCapInvalid || slipBeyondCap || slipInvalidSlice} on:click={submitOrder}>{submittingOrder ? 'Checking Kalshi…' : snapshot.health.tradingEnabled ? snapshot.health.mode === 'live' ? 'Review & place real order' : 'Place demo order' : 'Trading locked'}</button>
      {#if slipStatus}<p class="slip-status" aria-live="polite">{slipStatus}</p>{/if}
    </aside>
  {/if}
</div>
