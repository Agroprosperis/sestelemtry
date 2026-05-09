import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { PowerTooltip } from './PowerTooltip'

// Vitest doesn't auto-cleanup the rendered DOM between tests in this
// project's setup, so previous render outputs would leak across `it`
// blocks and trip multi-match assertions. Explicit cleanup keeps each
// test isolated.
afterEach(cleanup)

// PowerTooltip turns the raw recharts payload into the "детальна
// розшифровка" panel the user sees on the day chart. The interesting
// behavior — and the only thing we need to lock in here — is that
// active_ess_power_kw and grid_connected_active_power_kw get a
// sign-aware label / absolute value so analysts can tell charge from
// discharge and import from export without doing the sign math in
// their head.

function buildPayload(values: Record<string, number>) {
  return Object.entries(values).map(([dataKey, value]) => ({
    dataKey,
    name: dataKey,
    value,
    color: '#000',
  }))
}

describe('PowerTooltip directional labels', () => {
  it('renders ESS as Розряд with the absolute value when value is positive', () => {
    render(
      <PowerTooltip
        active
        label="14:35"
        payload={buildPayload({
          active_pv_power_kw: 97.12,
          // Positive ESS = battery delivering power = розряд on the
          // production firmware. The tooltip must say so explicitly
          // and show the magnitude as an absolute number.
          active_ess_power_kw: 3.5,
          grid_connected_active_power_kw: 100.56,
          load_power_kw: 197.68,
        })}
      />,
    )
    expect(screen.getByText('Розряд УЗЕ')).toBeInTheDocument()
    expect(screen.getByText(/^3[.,]5\s*kW$/)).toBeInTheDocument()
  })

  it('renders ESS as Заряд and grid as Експорт on negative values', () => {
    render(
      <PowerTooltip
        active
        label="20:00"
        payload={buildPayload({
          active_pv_power_kw: 0,
          // Negative ESS = battery absorbing power = заряд.
          active_ess_power_kw: -3.5,
          grid_connected_active_power_kw: -12.4,
          load_power_kw: 8.9,
        })}
      />,
    )
    expect(screen.getByText('Заряд УЗЕ')).toBeInTheDocument()
    expect(screen.getByText('Експорт у мережу')).toBeInTheDocument()
    expect(screen.getByText(/^3[.,]5\s*kW$/)).toBeInTheDocument()
    expect(screen.getByText(/^12[.,]4\s*kW$/)).toBeInTheDocument()
  })

  it('falls back to idle label when value is within the standby noise band', () => {
    render(
      <PowerTooltip
        active
        label="03:00"
        payload={buildPayload({
          active_pv_power_kw: 0,
          active_ess_power_kw: 0.01,
          grid_connected_active_power_kw: -0.02,
          load_power_kw: 0,
        })}
      />,
    )
    expect(screen.getByText('УЗЕ в очікуванні')).toBeInTheDocument()
    expect(screen.getByText('Точка приєднання (без обміну)')).toBeInTheDocument()
  })

  it('keeps inherently unsigned metrics (PV / load) free of charge/import-style relabeling', () => {
    render(
      <PowerTooltip
        active
        label="14:35"
        payload={buildPayload({
          active_pv_power_kw: 97.12,
          active_ess_power_kw: 0,
          grid_connected_active_power_kw: 0,
          load_power_kw: 197.68,
        })}
      />,
    )
    // PV active power and load power are inherently unsigned; the
    // tooltip must not graft a Заряд/Розряд/Імпорт/Експорт label
    // onto them just because their absolute value is large.
    expect(screen.queryByText(/Заряд УЗЕ|Розряд УЗЕ/)).toBeNull()
    expect(screen.queryByText(/Імпорт з мережі|Експорт у мережу/)).toBeNull()
    // Their numeric values land on the row as signed values (no abs
    // because we don't repaint them as directional metrics).
    expect(screen.getByText(/^97[.,]12\s*kW$/)).toBeInTheDocument()
    expect(screen.getByText(/^197[.,]68\s*kW$/)).toBeInTheDocument()
  })
})
