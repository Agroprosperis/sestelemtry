import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EMPTY_FLOWS, type EnergyFlows } from '../transforms/flows'
import { DailySummaryNarrative } from './DailySummaryNarrative'

function makeFlows(): EnergyFlows {
  return {
    ...EMPTY_FLOWS,
    pvProducedKwh: 555,
    pvToLoadKwh: 185,
    pvToGridKwh: 317,
    pvToEssKwh: 53.9,
    gridToLoadKwh: 91,
    gridToEssKwh: 3.47,
    essToLoadKwh: 2.06,
  }
}

const anchor = new Date('2026-06-01T00:00:00+03:00')

describe('DailySummaryNarrative', () => {
  it('shows placeholders before /energy-summary has returned', () => {
    render(
      <DailySummaryNarrative
        flows={EMPTY_FLOWS}
        preset="day"
        anchor={anchor}
        debug={false}
        registers={null}
        pvForecastTotal={null}
        loading
        flowsLoaded={false}
      />,
    )
    expect(screen.getByText(/прогноз —/)).toBeInTheDocument()
    expect(screen.getAllByText('—').length).toBeGreaterThan(2)
  })

  it('keeps the period numbers on screen during a background refresh', () => {
    // The on-the-fly allocator can take 5–15 s on a busy day; we
    // explicitly do NOT blank the card while a refresh is in flight
    // — the previous values stay valid and the header spinner
    // already signals "fresh data coming".
    render(
      <DailySummaryNarrative
        flows={makeFlows()}
        preset="day"
        anchor={anchor}
        debug={false}
        registers={null}
        pvForecastTotal={600}
        loading
        flowsLoaded
      />,
    )
    // Hero figure + 5 segment rows + 2 segment-bar totals + forecast
    // line all produce "кВт·год" strings; exact decimal formatting
    // is jsdom-locale-dependent.
    expect(screen.getAllByText(/кВт·год/).length).toBeGreaterThanOrEqual(6)
    expect(screen.getByText(/прогноз/)).toBeInTheDocument()
    expect(screen.queryAllByText('—').length).toBe(0)
  })
})
