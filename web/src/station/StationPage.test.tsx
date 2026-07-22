import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PlantInventory } from '../types'
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

describe('StationPage', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('shows empty state when API returns 404', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).includes('plant-inventory')) {
          return new Response('missing', { status: 404 })
        }
        return new Response('{}', { status: 200 })
      }),
    )
    render(<StationPage />)
    expect(
      await screen.findByText(/Ще немає знімка/i),
    ).toBeInTheDocument()
  })

  it('renders passport values from a snapshot', async () => {
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
      quality_flags: ['CONTROL_MODE_NOT_REMOTE'],
    }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (String(url).includes('plant-inventory')) {
          return new Response(JSON.stringify(body), { status: 200 })
        }
        return new Response('{}', { status: 200 })
      }),
    )
    render(<StationPage />)
    expect(await screen.findByText('Номінальна потужність СЕС')).toBeInTheDocument()
    expect(screen.getByText('450')).toBeInTheDocument()
    expect(screen.getByText(/Remote communication scheduling/)).toBeInTheDocument()
    expect(screen.getByText(/Режим керування не Remote/)).toBeInTheDocument()
  })
})
