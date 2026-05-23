import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../useOverviewData', () => ({
  useOverviewData: vi.fn(() => ({
    organizationID: 'pe',
    anchor: new Date(2026, 4, 23),
    flows: {
      pvToLoadKwh: 254,
      pvToEssKwh: 119,
      pvToGridKwh: 2570,
      gridToLoadKwh: 0,
      gridToEssKwh: 9,
      essToLoadKwh: 68,
      essToGridKwh: 0,
      pvProducedKwh: 2880,
      gridImportKwh: 9,
      gridExportKwh: 2570,
      essChargedKwh: 128,
      essDischargedKwh: 68,
      loadConsumedKwh: 322,
    },
    dayTotals: {
      pvProducedKwh: 2880,
      gridImportKwh: 9,
      gridExportKwh: 2570,
      essChargedKwh: 128,
      essDischargedKwh: 68,
      consumptionKwh: 322,
      pvSelfConsumedKwh: 373,
    },
    hourly: [],
    socPercent: 90,
    cumulative: {
      pvProducedKwh: 1_160_510,
      consumptionKwh: 1_815_750,
      gridImportKwh: 793_740,
      gridExportKwh: 138_500,
      essChargedKwh: 138_420,
      essDischargedKwh: 123_690,
      gridSupplyKwh: 138_420,
      referenceAt: new Date(2026, 3, 30),
    },
    pvForecastKwh: 3930,
    loading: false,
    error: null,
  })),
}))

import { OverviewPage } from '../OverviewPage'

afterEach(cleanup)

describe('OverviewPage', () => {
  it('renders the six core panels with their headings', () => {
    render(<OverviewPage />)
    expect(
      screen.getByRole('heading', { name: 'Сьогоднішній енергобаланс' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', { name: 'Підсумок за день' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', { name: 'Батарея за день' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', { name: 'Перетік за день (батарея)' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', { name: 'Накопичувальні показники' }),
    ).toBeInTheDocument()
  })

  it('shows the SOC value and forecast comparison from the data hook', () => {
    render(<OverviewPage />)
    expect(screen.getByText('90%')).toBeInTheDocument()
    // 2880 actual / 3930 forecast ≈ 73%
    expect(screen.getByText('73%')).toBeInTheDocument()
  })

  it('renders the Огляд/Детально view switch with Огляд active', () => {
    render(<OverviewPage />)
    const overviewBtn = screen.getByRole('button', {
      name: /Огляд/,
      pressed: true,
    })
    expect(overviewBtn).toBeInTheDocument()
  })
})
