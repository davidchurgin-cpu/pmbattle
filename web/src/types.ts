export type Money = number

export interface Participant { id: string; rotation: string; name: string; abbreviation: string }
export interface PriceQuote { exchange: string; ticker: string; outcome: string; rawPrice: Money; makerFee: Money; takerFee: Money; allInCost: Money; rawMoneyline: number; allInMoneyline: number; availableQuantity: Money }
export interface MarketView { type: 'moneyline' | 'spread' | 'total'; line?: string; away?: PriceQuote; home?: PriceQuote; over?: PriceQuote; under?: PriceQuote; status: string }
export interface Event { id: string; sport: string; league: string; startTime: string; status: string; participants: Participant[]; markets?: MarketView[] }
export interface Health { status: string; mode: string; scheduleUpdated: string; exchangeState: string; latencyMs: number; tradingEnabled: boolean }
export interface Order { id: string; exchange: string; ticker: string; rotation?: string; market: string; status: string; quantity: Money; filledQuantity: Money; limitPrice: Money; cashRisk: Money; createdAt: string }
export interface Fill { id: string; exchange: string; ticker: string; rotation?: string; team?: string; market: string; quantity: Money; rawPrice: Money; allInMoneyline: number; fee: Money; cashRisk: Money; createdAt: string }
export interface Position { exchange: string; ticker: string; rotation?: string; market: string; quantity: Money; cashRisk: Money; averagePrice: Money; currentPrice: Money; unrealizedPnl: Money }
export interface Snapshot { events: Event[]; orders: Order[]; positions: Position[]; fills: Fill[]; health: Health; bankroll: Money; atRisk: Money }
export interface BookLevel { price: Money; quantity: Money }
export interface OrderBook { ticker: string; sequence: number; updatedAt: string; stale: boolean; yes: BookLevel[]; no: BookLevel[] }

