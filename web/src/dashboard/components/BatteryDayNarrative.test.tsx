import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CurrentResponse } from '../../types'
import { EMPTY_FLOWS, type EnergyFlows } from '../transforms/flows'
import { BatteryDayNarrative } from './BatteryDayNarrative'

function makeCurrent(socPercent: number): CurrentResponse {
  return {
    organization_id: 'ze',
    metrics: {
      soc_percent: {
        metric_key: 'soc_percent',
        value: socPercent,
        time: '2026-06-01T07:00:00Z',
      },
    },
  }
}

function makeFlows(): EnergyFlows {
  return {
    ...EMPTY_FLOWS,
    pvToEssKwh: 53.9,
    gridToEssKwh: 3.47,
    essToLoadKwh: 2.06,
    essToGridKwh: 0,
  }
}

const anchor = new Date('2026-06-01T00:00:00+03:00')

describe('BatteryDayNarrative', () => {
  it('shows placeholders and the spinner when nothing has loaded yet', () => {
    render(
      <BatteryDayNarrative
        flows={EMPTY_FLOWS}
        current={null}
        preset="day"
        anchor={anchor}
        refreshing
        onRefresh={vi.fn()}
        loading
        flowsLoaded={false}
      />,
    )
    expect(screen.getAllByText('—').length).toBeGreaterThan(3)
    expect(screen.queryByText(/53,9 кВт·год/)).toBeNull()
    expect(
      screen.getByLabelText('Завантаження стану батареї'),
    ).toBeInTheDocument()
  })

  it('keeps SOC visible across a /current refresh', () => {
    // Once SOC has arrived once the operator should never lose
    // sight of it; the spinner in the header is enough of a cue
    // that a fresh sample is on its way.
    render(
      <BatteryDayNarrative
        flows={EMPTY_FLOWS}
        current={makeCurrent(52)}
        preset="day"
        anchor={anchor}
        refreshing={false}
        onRefresh={vi.fn()}
        loading
        flowsLoaded={false}
      />,
    )
    expect(screen.getByText('52%')).toBeInTheDocument()
  })

  it('keeps flow values visible during a background refresh and hides the spinner', () => {
    // Stale-while-revalidate: flowsLoaded=true means we have valid
    // previous numbers, so the card stays populated even when
    // refreshing=true (e.g. the operator clicked "Оновити перетік").
    // The in-header LoadingSpinner is suppressed because we have
    // data; the refresh button's own "is-spinning" indicator is
    // enough of a cue that something is happening.
    render(
      <BatteryDayNarrative
        flows={makeFlows()}
        current={makeCurrent(52)}
        preset="day"
        anchor={anchor}
        refreshing
        onRefresh={vi.fn()}
        loading={false}
        flowsLoaded
      />,
    )
    expect(screen.getAllByText(/кВт·год/).length).toBeGreaterThanOrEqual(6)
    expect(screen.queryAllByText('—').length).toBe(0)
    expect(screen.getByText('52%')).toBeInTheDocument()
    expect(
      screen.queryByLabelText('Завантаження стану батареї'),
    ).toBeNull()
  })

  it('renders the balance description once flows are loaded', () => {
    render(
      <BatteryDayNarrative
        flows={makeFlows()}
        current={makeCurrent(52)}
        preset="day"
        anchor={anchor}
        refreshing={false}
        onRefresh={vi.fn()}
        loading={false}
        flowsLoaded
      />,
    )
    // charged = 53.9 + 3.47 = 57.37; discharged = 2.06; balance = +55.31.
    // The description suffix is only printed when flowsLoaded, so a
    // single match proves the gating works.
    expect(
      screen.getAllByText(/більше заряду, ніж розряду/).length,
    ).toBeGreaterThanOrEqual(1)
  })
})
