import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PlantInventory, PlantInventoryHistory } from '../types'
import { StationPage } from './StationPage'

vi.mock('../dashboard/hooks/useOrganizationParam', () => ({
  useOrganizationParam: () => ({
    organizationID: 'ab',
    options: ['ab'],
    change: vi.fn(),
  }),
}))

vi.mock('../dashboard/config', () => ({
  formatOrganizationLabel: (id: string) => id,
}))

function stubInventoryApis(inv: PlantInventory | null, hist: PlantInventoryHistory) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      const u = String(url)
      if (u.includes('plant-inventory/history')) {
        return new Response(JSON.stringify(hist), { status: 200 })
      }
      if (u.includes('plant-inventory')) {
        if (inv == null) return new Response('missing', { status: 404 })
        return new Response(JSON.stringify(inv), { status: 200 })
      }
      return new Response('{}', { status: 200 })
    }),
  )
}

describe('StationPage', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows empty state when API returns 404', async () => {
    stubInventoryApis(null, { organization_id: 'ab', changes: {} })
    render(<StationPage />)
    expect(await screen.findByText(/Ще немає знімка/i)).toBeInTheDocument()
  })

  it('renders passport cards and expands history', async () => {
    const body: PlantInventory = {
      organization_id: 'ab',
      time: '2026-07-22T10:00:00Z',
      poll_reason: 'startup',
      pv_rated_kw: 450,
      ess_rated_kw: 864,
      ess_rated_kwh: 1720,
      ess_count: 8,
      pcs_count: 8,
      ess_soh_pct: 99.5,
      active_power_control_mode: 4,
      quality_flags: [],
    }
    const hist: PlantInventoryHistory = {
      organization_id: 'ab',
      changes: {
        pv_rated_kw: [
          { at: '2026-07-20T00:00:00Z', from: 400, to: 450, poll_reason: 'daily' },
        ],
      },
    }
    stubInventoryApis(body, hist)
    render(<StationPage />)
    expect(await screen.findByText('Номінальна потужність СЕС')).toBeInTheDocument()
    expect(screen.getByText('450')).toBeInTheDocument()
    expect(screen.getByText('1 змін')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Номінальна потужність СЕС/i }))
    expect(await screen.findByText('добовий')).toBeInTheDocument()
    expect(screen.getByText('400')).toBeInTheDocument()
  })
})
