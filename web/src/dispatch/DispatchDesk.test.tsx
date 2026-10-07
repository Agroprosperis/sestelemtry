import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DeskState, SimResult } from './dispatchClient'

const api = vi.hoisted(() => ({
  fetchDeskState: vi.fn(),
  previewDraft: vi.fn(),
  confirmDraft: vi.fn(),
}))
vi.mock('./dispatchClient', () => api)

import { DispatchDesk } from './DispatchDesk'

const cfg = { reserve_pct: 20, grid_charge: true, ess_sale: true, block_export: false, import_cap_kw: 1700, export_cap_kw: 850 }

function unknownResult(): SimResult {
  return {
    hours: Array.from({ length: 24 }, (_, i) => ({
      p_kw: null, wanted_kw: null, soc_pct: null, soc_before_pct: i === 0 ? 50 : null, import_kw: null, export_kw: null,
      curtailed_kw: null, charge_kw: 0, discharge_kw: 0, reasons: [], unknown: true,
    })),
    issues: [],
    known_hours: 0,
    end_soc_pct: null,
    min_soc_pct: null,
    peak_import_kw: null,
  }
}

function knownFirstHour(): SimResult {
  const r = unknownResult()
  r.hours[0] = {
    p_kw: 200, wanted_kw: 200, soc_pct: 38, soc_before_pct: 50, import_kw: 0, export_kw: 0,
    curtailed_kw: 0, charge_kw: 0, discharge_kw: 200, reasons: [], unknown: false,
  }
  r.known_hours = 1
  return r
}

function deskState(version = 0): DeskState {
  const start = Date.parse('2026-10-07T16:00:00Z')
  return {
    site_id: 'ze',
    timezone: 'Europe/Kyiv',
    now: '2026-10-07T16:20:00Z',
    start_hour: '2026-10-07T16:00:00Z',
    history_hours: 8,
    future_hours: 24,
    site: {
      capacity_kwh: 1720, charge_kw: 864, discharge_kw: 864, import_kw: 1700, import_set: true,
      soc_min_pct: 20, soc_max_pct: 90, eta: 0.9555, pv_rated_kw: 600, export_regime: 'self_production', export_ceiling_kw: 850,
    },
    tariffs: {
      distribution_uah_per_kwh: 2.75, transmission_uah_per_kwh: 0.74, supplier_margin_uah_per_kwh: 0, supplier_margin_mode: 'abs',
      supplier_margin_pct: 0, other_fees_uah_per_kwh: 0, export_discount: 0.05, degradation_uah_per_kwh: 0.6, include_vat: false,
      vat_rate: 0.2, roundtrip_efficiency: 0.913,
    },
    start_soc_pct: 50,
    start_soc_known: true,
    hours: Array.from({ length: 32 }, (_, k) => {
      const offset = k - 8
      return {
        ts: new Date(start + offset * 3_600_000).toISOString(),
        offset,
        rdn_uah_per_kwh: 6,
        buy_uah_per_kwh: 9.5,
        sell_uah_per_kwh: 5.7,
        pv_kw: 0,
        fact: offset < 0 ? { pv_kw: 10, load_kw: 300, grid_kw: 5, ess_kw: 285, soc_pct: 60 - k } : undefined,
      }
    }),
    defaults: cfg,
    applied: {
      version,
      confirmed_at: null,
      confirmed_by: '',
      model: { loads: Array(24).fill(null), commands: Array(24).fill(null), cfg },
    },
    result: unknownResult(),
  }
}

describe('DispatchDesk', () => {
  beforeEach(() => {
    window.localStorage.clear()
    api.fetchDeskState.mockReset()
    api.previewDraft.mockReset()
    api.confirmDraft.mockReset()
    api.fetchDeskState.mockResolvedValue(deskState())
    api.previewDraft.mockResolvedValue({ start_hour: '2026-10-07T16:00:00Z', result: knownFirstHour() })
  })

  it('stages load, reviews it and confirms the draft', async () => {
    render(<DispatchDesk site="ze" />)
    expect(await screen.findByText('Ручне керування УЗЕ')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Споживання для вибраних годин'), { target: { value: '200' } })
    fireEvent.click(screen.getByRole('button', { name: 'Задати' }))
    expect(screen.getByText(/Споживання 19:00–20:00 додано у чернетку/)).toBeInTheDocument()

    await waitFor(() => expect(api.previewDraft).toHaveBeenCalled())
    const review = screen.getByRole('button', { name: 'Переглянути чернетку →' })
    expect(review).toBeEnabled()
    fireEvent.click(review)
    expect(screen.getByText('Споживання: 200 кВт')).toBeInTheDocument()

    api.confirmDraft.mockResolvedValue({ status: 200, body: { version: 1, publish: { manifest_id: 'ze-x', published: true, intervals: 1 } } })
    api.fetchDeskState.mockResolvedValue(deskState(1))
    const confirm = await screen.findByRole('button', { name: 'Підтвердити' })
    await waitFor(() => expect(confirm).toBeEnabled())
    fireEvent.click(confirm)
    await waitFor(() => expect(api.confirmDraft).toHaveBeenCalled())
    const body = api.confirmDraft.mock.calls[0][1]
    expect(body.base_version).toBe(0)
    expect(body.loads[0]).toBe(200)
    expect(body.start_hour).toBe('2026-10-07T16:00:00Z')
    expect(await screen.findByText(/Застосовано · версія 1/)).toBeInTheDocument()
  })

  it('blocks «У чернетку» on an invalid command without a round trip', async () => {
    render(<DispatchDesk site="ze" />)
    await screen.findByText('Ручне керування УЗЕ')
    const param = screen.getByDisplayValue('300')
    fireEvent.change(param, { target: { value: '900' } })
    expect(screen.getByText('Паспортна межа розряду — 864 кВт.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'У чернетку' })).toBeDisabled()
  })

  it('shows the server shortfalls when confirm is refused', async () => {
    render(<DispatchDesk site="ze" />)
    await screen.findByText('Ручне керування УЗЕ')
    fireEvent.change(screen.getByLabelText('Споживання для вибраних годин'), { target: { value: '200' } })
    fireEvent.click(screen.getByRole('button', { name: 'Задати' }))
    await waitFor(() => expect(api.previewDraft).toHaveBeenCalled())
    fireEvent.click(screen.getByRole('button', { name: 'Переглянути чернетку →' }))
    api.confirmDraft.mockResolvedValue({ status: 409, body: { version: 0, conflict: true, message: 'План змінив інший користувач.' } })
    const confirm = await screen.findByRole('button', { name: 'Підтвердити' })
    await waitFor(() => expect(confirm).toBeEnabled())
    fireEvent.click(confirm)
    expect(await screen.findByText('План змінив інший користувач.')).toBeInTheDocument()
  })
})
