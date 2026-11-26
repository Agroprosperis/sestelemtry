import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ fetchOffgridReserve: vi.fn() }))
vi.mock('./offgridClient', () => api)

import { OffgridBlock } from './OffgridBlock'

const settings = {
  apply: false,
  island_soc_floor_pct: 20,
  outage_reserve_max_pct: 90,
  outage_error_margin_pct: 20,
  extra_stop_threshold_uah: 1000,
  generator_min_kw: 220,
  generator_target_kw: 480,
  generator_rated_kw: 734,
  island_power_max_kw: 576,
  generator_switch_soc_pct: 30,
}

describe('OffgridBlock', () => {
  it('shows the charge for each outage and whether the plan holds it', async () => {
    api.fetchOffgridReserve.mockResolvedValue({
      site_id: 'ze',
      now: '2026-11-27T06:00:00Z',
      timezone: 'Europe/Kyiv',
      settings,
      reserves: [
        { start: '2026-11-27T08:00:00Z', end: '2026-11-27T10:00:00Z', kind: 'schedule', hours: [], source: 'plan', error_margin_pct: 20, need_kwh: 720, target_pct: 61.9, short: false },
        {
          start: '2026-11-27T12:00:00Z', end: '2026-11-27T18:00:00Z', kind: 'schedule', hours: [], source: 'avg7d', error_margin_pct: 20,
          need_kwh: 3600, target_pct: 90, short: true, runs_out_at: '2026-11-27T14:00:00Z',
        },
      ],
    })
    render(<OffgridBlock site="ze" />)
    expect(await screen.findByText(/^27\.11, 10:00–12:00/)).toBeInTheDocument()
    expect(screen.getByText('61.9 %')).toBeInTheDocument()
    expect(screen.getByText(/план енергетика, \+20 % на помилку плану/)).toBeInTheDocument()
    expect(screen.getByText(/батареї вистачить приблизно до 16:00, далі потрібен генератор/)).toBeInTheDocument()
    expect(screen.getAllByText(/у плані: лише показ/)).toHaveLength(2)
  })

  it('hides itself with no outages ahead', async () => {
    api.fetchOffgridReserve.mockResolvedValue({ site_id: 'ze', now: '', timezone: 'Europe/Kyiv', settings, reserves: [] })
    const { container } = render(<OffgridBlock site="ze" />)
    await new Promise((r) => setTimeout(r, 0))
    expect(container.querySelector('.d-offgrid')).toBeNull()
  })
})
