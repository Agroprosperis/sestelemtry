import { describe, expect, it } from 'vitest'
import { dailyTotals, type HourEconomicsRow } from '../compute'
import { dayFigures, dayFormulas } from '../components/dayFormulas'
import { UAH_MONEY } from '../currency'
import { DEFAULT_TARIFFS } from '../tariffs'

const WEAR = DEFAULT_TARIFFS.degradationUahPerKwh

type Flows = {
  pv?: number
  pvToLoad?: number
  pvToEss?: number
  pvToGrid?: number
  gridToLoad?: number
  gridToEss?: number
  essToLoad?: number
  essToGrid?: number
}

// hour prices one hour the way the server does without a generator.
function hour(h: number, imp: number, exp: number, x: Flows): HourEconomicsRow {
  const f = {
    pv: 0, pvToLoad: 0, pvToEss: 0, pvToGrid: 0, gridToLoad: 0, gridToEss: 0, essToLoad: 0, essToGrid: 0,
    ...x,
  }
  const gridImport = f.gridToLoad + f.gridToEss
  const gridExport = f.pvToGrid + f.essToGrid
  const essCharged = f.pvToEss + f.gridToEss
  const essDischarged = f.essToLoad + f.essToGrid
  const load = f.pvToLoad + f.gridToLoad + f.essToLoad
  const baselineCost = load * imp
  const actualCost = gridImport * imp - gridExport * exp + essDischarged * WEAR
  const essNet = f.essToLoad * imp + f.essToGrid * exp - f.gridToEss * imp - f.pvToEss * exp - essDischarged * WEAR
  return {
    hour: h,
    hourStart: `2026-11-02T${String(h).padStart(2, '0')}:00:00+02:00`,
    rdnUahPerKwh: 5,
    flow: {
      pv: f.pv, gridImport, gridExport, essCharged, essDischarged,
      pvToEss: f.pvToEss, gridToEss: f.gridToEss, essToLoad: f.essToLoad, essToGrid: f.essToGrid,
    },
    economics: {
      load, pvToLoad: f.pvToLoad, pvToGrid: f.pvToGrid, gridToLoad: f.gridToLoad,
      importPriceUahPerKwh: imp, exportPriceUahPerKwh: exp,
      baselineCost, actualCost, effect: baselineCost - actualCost, essNet,
    },
    essRemainingKwhStart: null,
    essCostBasisUahStart: null,
    essAvgCostUahPerKwhStart: null,
    essWithdrawnCostUah: null,
    essRealizedProfitUah: null,
    essCostBasisUahEnd: null,
    essAvgCostUahPerKwhEnd: null,
    essResidualKwhEnd: null,
  }
}

// A sunny noon that charges the battery from PV, and an evening it gives
// back to the load.
function day(): Array<HourEconomicsRow | null> {
  const rows: Array<HourEconomicsRow | null> = Array.from({ length: 24 }, () => null)
  rows[10] = hour(10, 6, 4, { pv: 100, pvToLoad: 60, pvToEss: 30, pvToGrid: 10 })
  rows[19] = hour(19, 9, 7, { essToLoad: 25, gridToLoad: 15 })
  return rows
}

function result(lines: { op?: string; value: string }[] | undefined): string | undefined {
  return lines?.filter((l) => l.op === '=').at(-1)?.value
}

describe('dayFormulas', () => {
  const rows = day()
  const t = dailyTotals(rows)
  const f = dayFigures(t, rows, DEFAULT_TARIFFS, UAH_MONEY, null)
  const fx = dayFormulas(t, f, rows, DEFAULT_TARIFFS, UAH_MONEY)

  it('splits the effect between PV and the battery without a remainder', () => {
    expect(t.effect).toBe(610)
    expect(t.essNet).toBe(90)
    expect(f.pvEffect + t.essNet).toBe(t.effect)
    // PV is credited with its charge of the battery at the export price.
    expect(f.pvToEssValue).toBe(120)
  })

  it('writes each figure as the card shows it', () => {
    expect(result(fx.effect.lines)).toBe(UAH_MONEY.format(t.effect))
    expect(result(fx.pvEffect.lines)).toBe(UAH_MONEY.format(520))
    expect(result(fx.essEffect.lines)).toBe(UAH_MONEY.format(90))
    expect(result(fx.actual.lines)).toBe(UAH_MONEY.format(t.actualCost))
    expect(result(fx.hourly.lines)).toBe(UAH_MONEY.format(610))
  })

  it('names the battery legs with their arithmetic', () => {
    const pvLines = fx.pvEffect.lines ?? []
    expect(pvLines.find((l) => l.label === 'Заряд УЗЕ від СЕС')).toMatchObject({ op: '+', value: UAH_MONEY.format(120) })
    const essLines = fx.essEffect.lines ?? []
    expect(essLines.find((l) => l.label === 'Зменшення імпорту')).toMatchObject({ value: UAH_MONEY.format(225) })
    expect(essLines.find((l) => l.label.startsWith('Заряд від СЕС'))).toMatchObject({ op: '−', value: UAH_MONEY.format(120) })
    // Wear closes the sum: 25 kWh discharged × 0,60.
    expect(essLines.find((l) => l.label === 'Знос УЗЕ')).toMatchObject({ op: '−', value: UAH_MONEY.format(15) })
  })

  it('works the ratios through with the day’s energy', () => {
    expect(result(fx.selfSuff.lines)).toBe('85,0%')
    expect(result(fx.cycles.lines)).toBe('0,12')
  })
})
