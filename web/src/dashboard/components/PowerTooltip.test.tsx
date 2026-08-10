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

  it('keeps PV unsigned and shows load as a positive consumption number', () => {
    render(
      <PowerTooltip
        active
        label="14:35"
        payload={buildPayload({
          active_pv_power_kw: 97.12,
          active_ess_power_kw: 0,
          grid_connected_active_power_kw: 0,
          // load is stored negated on the row (chart draws it below
          // zero as a sink). The tooltip flips the sign back so the
          // user reads consumption as a positive number.
          load_power_kw: -197.68,
        })}
      />,
    )
    expect(screen.queryByText(/Заряд УЗЕ|Розряд УЗЕ/)).toBeNull()
    expect(screen.queryByText(/Імпорт з мережі|Експорт у мережу/)).toBeNull()
    expect(screen.getByText(/^97[.,]12\s*kW$/)).toBeInTheDocument()
    expect(screen.getByText(/^197[.,]68\s*kW$/)).toBeInTheDocument()
  })

  it('shows load as positive even when raw value lands at zero', () => {
    render(
      <PowerTooltip
        active
        label="03:00"
        payload={buildPayload({
          active_pv_power_kw: 0,
          active_ess_power_kw: 0,
          grid_connected_active_power_kw: 0,
          load_power_kw: 0,
        })}
      />,
    )
    expect(screen.getAllByText(/^0\s*kW$/).length).toBeGreaterThan(0)
  })
})

// The AI recommendation shares active_ess_power_kw's sign convention, so
// it gets the same directional treatment — plus the optimizer's reason,
// which is carried on the chart row rather than drawn as its own series.
describe('PowerTooltip AI recommendation', () => {
  function withPlan(essKw: number, extra: Record<string, unknown> = {}) {
    return [
      { dataKey: 'ai_ess_power_kw', name: 'ai', value: essKw, color: '#db2777', payload: extra },
      ...Object.entries(extra)
        .filter(([k]) => k !== 'ai_reason_text')
        .map(([dataKey, value]) => ({ dataKey, name: dataKey, value, color: '#000' })),
    ]
  }

  it('names a negative recommendation as charge and shows the magnitude', () => {
    render(<PowerTooltip active label="02:05" payload={withPlan(-180.5)} />)
    expect(screen.getByText('ШІ: заряд УЗЕ')).toBeInTheDocument()
    expect(screen.getByText(/^180[.,]5\s*кВт$/)).toBeInTheDocument()
  })

  it('names a positive recommendation as discharge', () => {
    render(<PowerTooltip active label="20:05" payload={withPlan(120)} />)
    expect(screen.getByText('ШІ: розряд УЗЕ')).toBeInTheDocument()
  })

  it('shows the optimizer reason for the hour', () => {
    render(
      <PowerTooltip
        active
        label="02:05"
        payload={withPlan(-180.5, { ai_reason_text: 'Заряд з мережі — дешева година' })}
      />,
    )
    expect(screen.getByText('Заряд з мережі — дешева година')).toBeInTheDocument()
  })

  it('renders the planned SOC only on the bucket that carries it', () => {
    render(<PowerTooltip active label="02:55" payload={withPlan(-180.5, { ai_soc_pct: 74.5 })} />)
    expect(screen.getByText('SOC за планом ШІ')).toBeInTheDocument()

    cleanup()
    render(<PowerTooltip active label="02:05" payload={withPlan(-180.5)} />)
    expect(screen.queryByText('SOC за планом ШІ')).toBeNull()
  })

  it('flips the negated recommended load back to positive consumption', () => {
    render(
      <PowerTooltip
        active
        label="12:05"
        payload={[
          { dataKey: 'ai_load_kw', name: 'ai load', value: -65.5, color: '#d97706', payload: {} },
        ]}
      />,
    )
    expect(screen.getByText('ШІ: споживання')).toBeInTheDocument()
    expect(screen.getByText(/^65[.,]5\s*кВт$/)).toBeInTheDocument()
  })

  it('stays out of the way when there is no recommendation', () => {
    render(
      <PowerTooltip
        active
        label="14:35"
        payload={buildPayload({ active_ess_power_kw: 3.5, load_power_kw: -100 })}
      />,
    )
    expect(screen.queryByText(/^ШІ:/)).toBeNull()
  })
})
