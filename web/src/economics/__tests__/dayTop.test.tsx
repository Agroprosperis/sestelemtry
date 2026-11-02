import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { EconomicsDayTop } from '../components/EconomicsDayTop'
import { dailyTotals, type HourEconomicsRow } from '../compute'
import { DEFAULT_TARIFFS } from '../tariffs'

function gridHour(hour: number, loadKwh: number): HourEconomicsRow {
  return {
    hour,
    hourStart: `2026-11-02T${String(hour).padStart(2, '0')}:00:00+02:00`,
    rdnUahPerKwh: 5,
    flow: {
      pv: 0,
      gridImport: loadKwh,
      gridExport: 0,
      essCharged: 0,
      essDischarged: 0,
      pvToEss: 0,
      gridToEss: 0,
      essToLoad: 0,
      essToGrid: 0,
    },
    economics: {
      load: loadKwh,
      pvToLoad: 0,
      pvToGrid: 0,
      gridToLoad: loadKwh,
      importPriceUahPerKwh: 5,
      exportPriceUahPerKwh: 4,
      baselineCost: loadKwh * 5,
      actualCost: loadKwh * 5,
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
  }
}

describe('EconomicsDayTop', () => {
  // The rest of today comes back as all-zero rows that already carry
  // the day's РДН price; they must not dilute the average.
  it('averages consumption over the hours that have data', () => {
    const rows = Array.from({ length: 24 }, (_, h) => gridHour(h, h === 0 ? 100 : h === 1 ? 300 : 0))
    render(<EconomicsDayTop totals={dailyTotals(rows)} rows={rows} tariffs={DEFAULT_TARIFFS} />)
    expect(screen.getByText(/Сер: 200,0 кВт/)).toBeInTheDocument()
    expect(screen.getByText(/Пік: 300,0 кВт/)).toBeInTheDocument()
  })
})
