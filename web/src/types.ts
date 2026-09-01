export type Money = number

export interface Participant { id: string; rotation: string; name: string; abbreviation: string }
export interface PriceQuote { exchange: string; ticker: string; outcome: string; side: 'yes' | 'no'; rawPrice: Money; makerFee: Money; takerFee: Money; allInCost: Money; rawMoneyline: number; allInMoneyline: number; availableQuantity: Money }
export interface MarketOption { line: string; away?: PriceQuote; home?: PriceQuote; over?: PriceQuote; under?: PriceQuote }
export interface MarketView { type: 'moneyline' | 'spread' | 'total'; line?: string; away?: PriceQuote; home?: PriceQuote; over?: PriceQuote; under?: PriceQuote; options?: MarketOption[]; status: string }
export interface Event { id: string; sport: string; league: string; startTime: string; status: string; participants: Participant[]; markets?: MarketView[] }
export interface Health { status: string; mode: string; scheduleUpdated: string; exchangeState: string; latencyMs: number; tradingEnabled: boolean }
export interface Order { id: string; exchange: string; ticker: string; rotation?: string; market: string; status: string; quantity: Money; filledQuantity: Money; limitPrice: Money; cashRisk: Money; createdAt: string }
export interface ChildOrderState { id: string; clientOrderId: string; status: string; quantity: Money; filledQuantity: Money; createdAt: string; updatedAt: string }
export interface ParentOrder { id: string; exchange: string; eventId: string; ticker: string; rotation?: string; outcome: string; market: string; side: string; strategy: string; policy: string; status: string; cashRiskTarget: Money; reservedRisk: Money; filledRisk: Money; remainingRisk: Money; priceCapMoneyline: number; limitPrice: Money; quantity: Money; filledQuantity: Money; sliceQuantity?: Money; childOrderIds: string[]; children?: ChildOrderState[]; processedFillIds?: string[]; createdAt: string; updatedAt: string }
export interface Fill { id: string; orderId?: string; exchange: string; ticker: string; rotation?: string; team?: string; market: string; quantity: Money; rawPrice: Money; allInMoneyline: number; fee: Money; cashRisk: Money; createdAt: string }
export interface Position { exchange: string; ticker: string; rotation?: string; market: string; quantity: Money; cashRisk: Money; averagePrice: Money; currentPrice: Money; unrealizedPnl: Money }
export interface SportOption { name: string; eventCount: number; addedGameCount: number; enabled: boolean }
export interface Settings { preferences: { enabledSports: string[] | null; excludeAddedGames: boolean }; availableSports: SportOption[] }
export interface Snapshot { events: Event[]; parentOrders: ParentOrder[]; orders: Order[]; positions: Position[]; fills: Fill[]; health: Health; bankroll: Money; atRisk: Money; settings: Settings }
export interface AccountSnapshot { parentOrders: ParentOrder[]; orders: Order[]; positions: Position[]; fills: Fill[]; bankroll: Money; atRisk: Money }
export interface BookLevel { price: Money; quantity: Money }
export interface OrderBook { ticker: string; sequence: number; updatedAt: string; stale: boolean; yes: BookLevel[]; no: BookLevel[] }
