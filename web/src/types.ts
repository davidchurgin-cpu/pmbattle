export type Money = number

export interface Participant { id: string; rotation: string; name: string; abbreviation: string }
export interface PriceQuote { exchange: string; ticker: string; outcome: string; side: 'yes' | 'no'; rawPrice: Money; makerFee: Money; takerFee: Money; allInCost: Money; rawMoneyline: number; allInMoneyline: number; availableQuantity: Money }
export interface MarketOption { line: string; away?: PriceQuote; home?: PriceQuote; over?: PriceQuote; under?: PriceQuote }
export interface MarketView { type: 'moneyline' | 'spread' | 'total'; line?: string; away?: PriceQuote; home?: PriceQuote; over?: PriceQuote; under?: PriceQuote; options?: MarketOption[]; status: string }
export interface Event { id: string; sport: string; league: string; startTime: string; status: string; participants: Participant[]; markets?: MarketView[] }
export interface Health { status: string; mode: string; scheduleUpdated: string; exchangeState: string; accountState: string; accountUpdated?: string; mappedMarkets: number; latencyMs: number; tradingEnabled: boolean; tradingLock?: string; maxCashRisk?: Money }
export interface Order { id: string; exchange: string; ticker: string; rotation?: string; market: string; status: string; quantity: Money; filledQuantity: Money; limitPrice: Money; cashRisk: Money; createdAt: string }
export interface ChildOrderState { id: string; clientOrderId: string; status: string; quantity: Money; filledQuantity: Money; createdAt: string; updatedAt: string }
export interface ParentOrder { id: string; exchange: string; eventId: string; ticker: string; rotation?: string; outcome: string; market: string; side: string; strategy: string; policy: string; status: string; cashRiskTarget: Money; reservedRisk: Money; filledRisk: Money; remainingRisk: Money; priceCapMoneyline: number; limitPrice: Money; quantity: Money; filledQuantity: Money; sliceQuantity?: Money; childOrderIds: string[]; children?: ChildOrderState[]; processedFillIds?: string[]; lastRepricedAt?: string; replaceCount?: number; createdAt: string; updatedAt: string }
export interface Fill { id: string; orderId?: string; exchange: string; ticker: string; rotation?: string; team?: string; market: string; quantity: Money; rawPrice: Money; allInMoneyline: number; fee: Money; cashRisk: Money; createdAt: string }
export interface Position { exchange: string; ticker: string; eventId?: string; rotation?: string; market: string; side?: string; quantity: Money; cashRisk: Money; totalTraded?: Money; averagePrice: Money; currentPrice: Money; realizedPnl?: Money; unrealizedPnl: Money; feesPaid?: Money; updatedAt?: string }
export interface Settlement { exchange: string; ticker: string; eventTicker?: string; eventId?: string; rotation?: string; market?: string; result: string; yesQuantity: Money; noQuantity: Money; yesTotalCost: Money; noTotalCost: Money; revenue: Money; fee: Money; netPnl: Money; settlementValue: Money; settledAt: string }
export interface AuditRecord { id: number; occurredAt: string; kind: string; payload: Record<string, unknown> }
export interface AuditPage { records: AuditRecord[]; nextBefore?: number; hasMore: boolean }
export interface MappingCandidate { eventId: string; sport: string; league: string; startTime: string; participants: Participant[]; score: number }
export interface MappingReview { id: string; exchange: string; title: string; occurrenceTime?: string; tickers: string[]; marketTypes: string[]; candidates: MappingCandidate[]; updatedAt: string }
export interface SportOption { name: string; eventCount: number; addedGameCount: number; enabled: boolean }
export interface Settings { preferences: { enabledSports: string[] | null; excludeAddedGames: boolean }; availableSports: SportOption[] }
export interface Snapshot { events: Event[]; parentOrders: ParentOrder[]; orders: Order[]; positions: Position[]; settlements: Settlement[]; fills: Fill[]; health: Health; bankroll: Money; availableToAllocate: Money; atRisk: Money; settings: Settings }
export interface AccountSnapshot { parentOrders: ParentOrder[]; orders: Order[]; positions: Position[]; settlements: Settlement[]; fills: Fill[]; bankroll: Money; availableToAllocate: Money; atRisk: Money }
export interface AccountSummary { bankroll: Money; availableToAllocate: Money; atRisk: Money }
export interface BookLevel { price: Money; quantity: Money }
export interface OrderBook { ticker: string; sequence: number; updatedAt: string; stale: boolean; yes: BookLevel[]; no: BookLevel[] }
