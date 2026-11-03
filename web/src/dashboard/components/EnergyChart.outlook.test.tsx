import { cloneElement, type ReactElement } from 'react'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { UzePlanResponse } from '../../api'
import { ThemeProvider } from '../../theme/theme'
import type { PowerChartRow } from '../transforms/power'
import { energySummaryFromTotals } from '../transforms/summary'
import { EnergyChart } from './EnergyChart'

// jsdom has no layout, so the real container measures 0×0 and recharts
// draws nothing — not even the legend these tests read.
vi.mock('recharts', async (importOriginal) => {
  const mod = await importOriginal<typeof import('recharts')>()
  return {
    ...mod,
    ResponsiveContainer: ({ children }: { children: ReactElement<{ width?: number; height?: number }> }) =>
      cloneElement(children, { width: 800, height: 400 }),
  }
})

const summary = energySummaryFromTotals({})

const plan: UzePlanResponse = {
  organization_id: 'ze',
  date: '2026-11-04',
  tz: 'Europe/Kyiv',
  available: true,
  soc_start_pct: 40,
  capacity_kwh: 200,
  power_kw: 100,
  hours: [
    {
      hour: 2,
      recommended_ess_kw: -40,
      soc_pct: 50,
      ess_to_load_kwh: 0,
      ess_to_grid_kwh: 0,
      pv_to_ess_kwh: 0,
      grid_to_ess_kwh: 40,
      effect_uah: 0,
      action: 'charge',
      reason_code: 'CHARGE_GRID_CHEAP',
      reason_text: 'дешева година',
      recommended_load_kw: 80,
      rdn_uah_per_kwh: 1,
    },
  ],
  totals: {
    optimum_uah: 0,
    fact_uah: 0,
    reserve_uah: 0,
    captured_share: 0,
    charge_pv_kwh: 0,
    charge_grid_kwh: 0,
    discharge_kwh: 0,
    export_val_uah: 0,
    load_val_uah: 0,
    charge_pv_cost_uah: 0,
    grid_cost_uah: 0,
    degradation_uah: 0,
  },
}

const measured: PowerChartRow[] = [{ time: '00:00', active_pv_power_kw: 12 }]

function chart(powerSeries: PowerChartRow[], overlay?: { defaultVisible: boolean; essLabel: string }) {
  return (
    <ThemeProvider>
      <EnergyChart
        summary={summary}
        loading={false}
        powerSeries={powerSeries}
        damSeries={[{ time: '02:00', bucketStart: 0, price: 1500 }]}
        pvForecastSeries={[{ hour: 12, plannedKw: 90 }]}
        aiPlan={plan}
        planOverlay={overlay}
      />
    </ThemeProvider>
  )
}

function legendHidden(label: string): string | null {
  return screen.getByRole('button', { name: label }).getAttribute('aria-pressed')
}

describe('EnergyChart outlook day', () => {
  it('draws tomorrow from the forecast, the prices and a visible recommendation', () => {
    render(chart([]))

    expect(screen.queryByText('No data available for selected range.')).toBeNull()
    expect(screen.queryByText('З електромережі')).toBeNull()
    expect(legendHidden('Рекомендація ШІ (УЗЕ)')).toBe('false')
  })

  it('keeps the recommendation opt-in on a measured day', () => {
    render(chart(measured))

    expect(screen.getByText('З електромережі')).toBeInTheDocument()
    expect(legendHidden('Рекомендація ШІ (УЗЕ)')).toBe('true')
  })

  it('restores the opt-in default when leaving tomorrow', () => {
    const { rerender } = render(chart([]))
    rerender(chart(measured))

    expect(legendHidden('Рекомендація ШІ (УЗЕ)')).toBe('true')
  })

  it('leaves the control-mode plan visible once telemetry arrives', () => {
    const overlay = { defaultVisible: true, essLabel: 'План УЗЕ (manifest)' }
    const { rerender } = render(chart([], overlay))
    rerender(chart(measured, overlay))

    expect(legendHidden('План УЗЕ (manifest)')).toBe('false')
  })
})
