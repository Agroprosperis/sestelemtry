import { cloneElement, type ReactElement } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { UzePlanResponse } from '../../api'
import { ThemeProvider } from '../../theme/theme'
import type { PowerChartRow } from '../transforms/power'
import type { DaySupplyBucket } from '../transforms/supply'
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

describe('EnergyChart planned outages', () => {
  it('draws tomorrow from its outage schedule alone', () => {
    const { container } = render(
      <ThemeProvider>
        <EnergyChart
          summary={summary}
          loading={false}
          powerSeries={[]}
          outageAreas={[{ x1: '04:00', x2: '06:00', kind: 'schedule' }]}
        />
      </ThemeProvider>,
    )

    expect(screen.queryByText('No data available for selected range.')).toBeNull()
    expect(container.querySelectorAll('.outage-marker')).toHaveLength(1)
    expect(legendHidden('Відключення за планом')).toBe('false')
  })

  it('keeps every kind of planned outage in the one plan bar', () => {
    render(
      <ThemeProvider>
        <EnergyChart
          summary={summary}
          loading={false}
          powerSeries={measured}
          outageAreas={[
            { x1: '04:00', x2: '06:00', kind: 'schedule' },
            { x1: '10:00', x2: '12:00', kind: 'other' },
          ]}
        />
      </ThemeProvider>,
    )

    expect(screen.getAllByRole('button', { name: /Відключення|без мережі/ })).toHaveLength(1)
    expect(legendHidden('Відключення за планом')).toBe('false')
  })

  it('marks each outage with a bar at the top, gone once hidden in the legend', () => {
    const wholeDay: PowerChartRow[] = Array.from({ length: 288 }, (_, i) => ({
      time: `${String(Math.floor(i / 12)).padStart(2, '0')}:${String((i % 12) * 5).padStart(2, '0')}`,
      active_pv_power_kw: 12,
    }))
    const { container } = render(
      <ThemeProvider>
        <EnergyChart
          summary={summary}
          loading={false}
          powerSeries={wholeDay}
          outageAreas={[
            { x1: '04:00', x2: '06:00', kind: 'schedule' },
            { x1: '10:00', x2: '12:00', kind: 'schedule' },
          ]}
        />
      </ThemeProvider>,
    )

    expect(container.querySelectorAll('.outage-marker')).toHaveLength(2)
    fireEvent.click(screen.getByRole('button', { name: 'Відключення за планом' }))
    expect(container.querySelectorAll('.outage-marker')).toHaveLength(0)
  })

  it('draws the stretches without the grid as a fact track, hidden apart from the plan', () => {
    const labels = Array.from(
      { length: 288 },
      (_, i) => `${String(Math.floor(i / 12)).padStart(2, '0')}:${String((i % 12) * 5).padStart(2, '0')}`,
    )
    const wholeDay: PowerChartRow[] = labels.map((time) => ({ time, active_pv_power_kw: 12 }))
    const supply: DaySupplyBucket[] = labels.map((time, i) => ({
      t: i,
      time,
      mode: (time >= '19:15' && time < '20:30') || (time >= '14:35' && time < '18:05') ? 'island' : 'grid',
    }))
    const { container } = render(
      <ThemeProvider>
        <EnergyChart
          summary={summary}
          loading={false}
          powerSeries={wholeDay}
          supplySeries={supply}
          outageAreas={[{ x1: '14:00', x2: '18:00', kind: 'schedule' }]}
        />
      </ThemeProvider>,
    )

    expect(container.querySelectorAll('.outage-actual')).toHaveLength(2)
    expect(legendHidden('Фактично без мережі')).toBe('false')
    fireEvent.click(screen.getByRole('button', { name: 'Фактично без мережі' }))
    expect(container.querySelectorAll('.outage-actual')).toHaveLength(0)
    expect(container.querySelectorAll('.outage-marker')).toHaveLength(1)
  })

  it('has no fact track for a day on the grid', () => {
    render(
      <ThemeProvider>
        <EnergyChart summary={summary} loading={false} powerSeries={measured} supplySeries={null} />
      </ThemeProvider>,
    )
    expect(screen.queryByRole('button', { name: 'Фактично без мережі' })).toBeNull()
  })
})
