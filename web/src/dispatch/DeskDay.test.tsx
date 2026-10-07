import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { DayResponse } from './dispatchClient'

const api = vi.hoisted(() => ({ fetchDeskDay: vi.fn() }))
vi.mock('./dispatchClient', () => api)

import { DeskDay } from './DeskDay'

const site = {
  capacity_kwh: 1720, charge_kw: 864, discharge_kw: 864, import_kw: 1700, import_set: true,
  soc_min_pct: 20, soc_max_pct: 90, eta: 0.9555, pv_rated_kw: 600, export_regime: 'self_production', export_ceiling_kw: 850,
}

function day(): DayResponse {
  // 2026-10-06 00:00 Kyiv = 2026-10-05T21:00Z.
  const start = Date.parse('2026-10-05T21:00:00Z')
  return {
    site_id: 'ze',
    timezone: 'Europe/Kyiv',
    date: '2026-10-06',
    site,
    hours: Array.from({ length: 24 }, (_, h) => ({
      ts: new Date(start + h * 3_600_000).toISOString(),
      rdn_uah_per_kwh: 5,
      buy_uah_per_kwh: 8.5,
      sell_uah_per_kwh: 4.75,
      fact: { pv_kw: 0, load_kw: 300, grid_kw: h >= 18 && h < 22 ? 20 : 300, ess_kw: h >= 18 && h < 22 ? 280 : 0, soc_pct: 60 },
      plan:
        h >= 18 && h < 22
          ? {
              ts: '', load_kw: 300, pv_kw: 0, ess_kw: 300, soc_pct: 55, import_kw: 0, export_kw: 0, curtailed_kw: 0,
              command: { type: 'cover' as const, value: 300, id: 'block-0-4-cover' },
            }
          : undefined,
    })),
  }
}

describe('DeskDay', () => {
  it('shows fact against the plan in force, read only', async () => {
    api.fetchDeskDay.mockResolvedValue(day())
    const onClose = vi.fn()
    const { container } = render(
      <DeskDay site="ze" date="2026-10-06" maxDate="2026-10-07" timezone="Europe/Kyiv" onDate={() => {}} onClose={onClose} />,
    )
    expect(await screen.findByText(/план\s+діяв 4 з 24 год/)).toBeInTheDocument()
    expect(screen.getByText(/Факт УЗЕ/).textContent).toContain('розряд 1\u00a0120')
    expect(container.querySelector('[data-mark="plan-ess"]')).not.toBeNull()
    expect(container.querySelector('[data-command-status]')).toBeNull()
    expect(screen.getByText(/00:00–01:00 · факт · плану не було/)).toBeInTheDocument()
    expect(api.fetchDeskDay).toHaveBeenCalledWith('ze', '2026-10-06', expect.anything())

    fireEvent.click(screen.getByRole('button', { name: '← До пульта' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('says so when the edge had no plan that day', async () => {
    const d = day()
    d.hours.forEach((h) => (h.plan = undefined))
    api.fetchDeskDay.mockResolvedValue(d)
    render(<DeskDay site="ze" date="2026-10-06" maxDate="2026-10-07" timezone="Europe/Kyiv" onDate={() => {}} onClose={() => {}} />)
    expect(await screen.findByText('Плану на цей день не було — edge працював на самоспоживання.')).toBeInTheDocument()
  })
})
