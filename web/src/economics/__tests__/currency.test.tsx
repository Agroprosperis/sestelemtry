import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import type { EconomicsMonthlyDayMargin } from '../../api'
import { EconomicsDayTop } from '../components/EconomicsDayTop'
import { EconomicsHeader } from '../components/EconomicsHeader'
import { dailyTotals, type HourEconomicsRow } from '../compute'
import { makeMoney, MoneyContext, parseCurrency, rateNote, UAH_MONEY } from '../currency'
import { MonthlyHeatmap } from '../monthly/EconomicsMonthlyView'
import { heatLegend, heatTier } from '../monthly/rollup'
import { axisTickLabel, capexResolver, capexStepsInCurrency, moneyAxis } from '../payback'
import { DEFAULT_TARIFFS } from '../tariffs'

// uk-UA groups thousands with a no-break space; compare on plain ones.
const plain = (s: string | null | undefined) => (s ?? '').replace(/\s/g, ' ')

const usd = makeMoney('USD', 41.5)

describe('parseCurrency', () => {
  it('reads ?currency= and falls back to UAH', () => {
    expect(parseCurrency('usd')).toBe('USD')
    expect(parseCurrency(' EUR ')).toBe('EUR')
    expect(parseCurrency('GBP')).toBe('UAH')
    expect(parseCurrency(null)).toBe('UAH')
  })
})

describe('makeMoney', () => {
  it('keeps UAH the way the page always wrote it', () => {
    expect(plain(UAH_MONEY.format(1234.4))).toBe('1 234 грн')
    expect(UAH_MONEY.formatPrice(5.432)).toBe('5,43')
    expect(UAH_MONEY.formatPrice(0)).toBe('—')
    expect(UAH_MONEY.formatPriceAllowZero(0)).toBe('0,00')
    expect(UAH_MONEY.formatCompactPrice(13.1)).toBe('13')
    expect(UAH_MONEY.priceUnit).toBe('грн/кВт·год')
    expect(UAH_MONEY.fromUah(83)).toBe(83)
  })

  it('keeps cents below a thousand dollars and three decimals on prices', () => {
    expect(usd.format(12.3)).toBe('12,30 $')
    expect(plain(usd.format(2500.4))).toBe('2 500 $')
    expect(usd.formatPrice(0.1321)).toBe('0,132')
    expect(usd.formatCompactPrice(0.3157)).toBe('0,32')
    expect(usd.priceUnit).toBe('$/кВт·год')
    expect(usd.fromUah(83)).toBe(2)
    expect(makeMoney('EUR', 48).unit).toBe('€')
  })

  it('names the rate behind the figures', () => {
    expect(rateNote(UAH_MONEY)).toBeNull()
    expect(rateNote(usd, { day: true })).toContain('Курс доби: 41,50 грн за 1 $ (АП Банк, на 12:00)')
    expect(rateNote(usd, { day: true, provisional: true })).toContain(
      'Поточний курс ERP: 41,50 грн за 1 $ (АП Банк, остаточний — о 12:00)',
    )
    expect(rateNote(usd)).toContain('Середньозважений курс за період (за EBITDA діб): 41,50 грн за 1 $')
    expect(rateNote(usd, { carriedDays: 3 })).toContain('3 дн. без власного курсу')
  })
})

describe('capexStepsInCurrency', () => {
  // Each stage's increment converts at the rate of its own date.
  const steps = [
    { effectiveFrom: '1970-01-01', capexUah: 99e6 },
    { effectiveFrom: '2025-06-20', capexUah: 21e6 },
    { effectiveFrom: '2026-03-15', capexUah: 31e6 },
  ]
  const rates: Record<string, number> = { '2025-06-20': 40, '2026-03-15': 50 }

  it('adds up increments at their dates', () => {
    const converted = capexStepsInCurrency(steps, (d) => rates[d] ?? null)
    expect(converted).toEqual([
      { effectiveFrom: '2025-06-20', capexUah: 525_000 },
      { effectiveFrom: '2026-03-15', capexUah: 725_000 },
    ])
    const capexAt = capexResolver(converted ?? [], NaN)
    expect(capexAt('2025-12')).toBe(525_000)
    expect(capexAt('2026-03')).toBe(725_000)
  })

  it('waits for every rate', () => {
    expect(capexStepsInCurrency(steps, (d) => (d === '2025-06-20' ? 40 : null))).toBeNull()
  })
})

describe('moneyAxis in another currency', () => {
  it('keeps plain dollars below ten thousand', () => {
    const small = moneyAxis(5_000, usd)
    expect(small.unit).toBe('$')
    expect(plain(axisTickLabel(small, 2_000))).toBe('2 000')
    const mid = moneyAxis(50_000, usd)
    expect(mid.unit).toBe('тис. $')
    expect(axisTickLabel(mid, 12_500)).toBe('12,5 тис.')
    expect(moneyAxis(2_000_000, usd).unit).toBe('млн $')
  })
})

describe('heat bands', () => {
  it('move with the rate, so the same margin keeps its colour', () => {
    expect(heatTier(13.1)).toBe('economics-hm3')
    expect(heatTier(13.1 / 41.5, usd)).toBe('economics-hm3')
    expect(heatTier(1.9 / 41.5, usd)).toBe('economics-hm0')
    expect(heatLegend(usd)).toContain('сірий до 0,048')
    expect(heatLegend(usd)).toContain('$/кВт·год')
  })

  it('render a dollar heatmap cell in cents', () => {
    const row: EconomicsMonthlyDayMargin = {
      date: '2026-08-03',
      hours: Array.from({ length: 24 }, (_, h) =>
        h === 20
          ? { margin_uah_per_kwh: 13.1 / 41.5, discharged_kwh: 42, revenue_uah: 17, cost_uah: 3, wear_uah: 0.6 }
          : null,
      ),
    }
    const { container } = render(
      <MoneyContext.Provider value={usd}>
        <MonthlyHeatmap margins={[row]} />
      </MoneyContext.Provider>,
    )
    const cell = container.querySelectorAll('tbody td')[20]
    expect(cell.textContent).toBe('0,32')
    expect(cell.className).toBe('economics-hm3')
    expect(cell.getAttribute('title')).toContain('$/кВт·год')
  })
})

describe('EconomicsHeader currency switch', () => {
  it('offers ₴ / $ / € and reports the choice', () => {
    const onCurrencyChange = vi.fn()
    render(
      <EconomicsHeader
        range="month"
        onRangeChange={() => {}}
        windowFrom=""
        windowTo=""
        onWindowChange={() => {}}
        date="2026-08-03"
        onDateChange={() => {}}
        currency="UAH"
        onCurrencyChange={onCurrencyChange}
      />,
    )
    expect(screen.getByRole('button', { name: 'Гривні' })).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(screen.getByRole('button', { name: 'Долари США' }))
    expect(onCurrencyChange).toHaveBeenCalledWith('USD')
  })
})

describe('EconomicsDayTop in dollars', () => {
  // The rows arrive converted; the tariff components the page holds in
  // UAH convert at the day's rate.
  it('converts the network legs at the day rate', () => {
    const hour = (h: number, load: number): HourEconomicsRow => ({
      hour: h,
      hourStart: `2026-11-02T${String(h).padStart(2, '0')}:00:00+02:00`,
      rdnUahPerKwh: 0.1,
      flow: { pv: 0, gridImport: load, gridExport: 0, essCharged: 0, essDischarged: 0, pvToEss: 0, gridToEss: 0, essToLoad: 0, essToGrid: 0 },
      economics: {
        load,
        pvToLoad: 0,
        pvToGrid: 0,
        gridToLoad: load,
        importPriceUahPerKwh: 0.12,
        exportPriceUahPerKwh: 0.1,
        baselineCost: load * 0.12,
        actualCost: load * 0.12,
        effect: 0,
        essNet: 0,
      },
      essRemainingKwhStart: null,
      essCostBasisUahStart: null,
      essAvgCostUahPerKwhStart: null,
      essWithdrawnCostUah: null,
      essRealizedProfitUah: null,
      essCostBasisUahEnd: null,
      essAvgCostUahPerKwhEnd: null,
      essResidualKwhEnd: null,
    })
    const rows = Array.from({ length: 24 }, (_, h) => hour(h, h === 0 ? 1000 : 0))
    render(
      <MoneyContext.Provider value={usd}>
        <EconomicsDayTop totals={dailyTotals(rows)} rows={rows} tariffs={DEFAULT_TARIFFS} />
      </MoneyContext.Provider>,
    )
    const transmission = screen.getByText('Передача:').nextSibling?.textContent
    expect(transmission).toBe(usd.format((1000 * DEFAULT_TARIFFS.transmissionUahPerKwh) / 41.5))
    expect(screen.getAllByText('$/кВт·год').length).toBeGreaterThan(0)
  })
})
