import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ fetchOffgridReserve: vi.fn(), fetchOffgridAdvice: vi.fn(), fetchOffgridOutages: vi.fn() }))
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

const noAdvice = { site_id: 'ze', now: '', timezone: 'Europe/Kyiv', live: { online: true, mode: 'grid' } }

describe('OffgridBlock', () => {
  beforeEach(() => {
    api.fetchOffgridReserve.mockReset()
    api.fetchOffgridAdvice.mockReset()
    api.fetchOffgridOutages.mockReset()
    api.fetchOffgridAdvice.mockResolvedValue(noAdvice)
  })

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

  it('shows the advice, the power at hand and plan against fact on demand', async () => {
    api.fetchOffgridReserve.mockResolvedValue({ site_id: 'ze', now: '', timezone: 'Europe/Kyiv', settings, reserves: [] })
    api.fetchOffgridAdvice.mockResolvedValue({
      ...noAdvice,
      available: { island_kw: 626, generator_kw: 1534, autonomy_now_min: 165, start_generator_by: '2026-11-27T10:31:00Z' },
      outage: {
        start: '2026-11-27T08:00:00Z', end: '2026-11-27T16:00:00Z', kind: 'schedule', final: false, soc_pct: 60,
        recharge_uah_per_kwh: 5, recharge_estimated: true,
        advice: {
          recommended: 'C',
          why: 'найдешевший із можливих',
          variants: [
            { id: 'A', label: 'Острів на все відключення', feasible: false, why: 'заряду не вистачить до кінця', cost_uah: null, fuel_l: 0, stops: 2, end_soc_pct: 0 },
            { id: 'B', label: 'Генератор одразу', feasible: true, cost_uah: 34320, fuel_l: 624, stops: 2, end_soc_pct: 60 },
            { id: 'C', label: 'Острів, потім генератор', feasible: true, cost_uah: 29521, fuel_l: 490, stops: 3, end_soc_pct: 30, switch_at: '2026-11-27T09:43:00Z' },
          ],
        },
      },
    })
    api.fetchOffgridOutages.mockResolvedValue({
      site_id: 'ze', from: '2026-11-01', to: '2026-11-28', timezone: 'Europe/Kyiv', error_margin_pct: 20,
      summary: { outages: 1, unscheduled: 0, hours_offgrid: 2, fuel_l: 57, fuel_uah: 3180, cost_fact_uah: 4180, cost_advised_uah: 3900, with_plan: 1 },
      records: [
        {
          kind: 'schedule', happened: true, start: '2026-11-15T08:30:00Z', end: '2026-11-15T10:15:00Z', sequence: ['острів', 'генератор'],
          minutes_generator: 55, minutes_island: 50, minutes_blackout: 4, stops: 3, gen_kwh: 220, gen_below_min_minutes: 15, fuel_l: 57,
          fuel_uah: 3180, ess_kwh: 200, load_fact_kwh: 470, load_plan_kwh: 437.5, deviation_pct: 7.4, cost_fact_uah: 4180, advised: 'C',
        },
      ],
    })
    render(<OffgridBlock site="ze" />)
    expect(await screen.findByText(/Острів зараз: до 626 кВт/)).toBeInTheDocument()
    expect(screen.getByText(/генератор запускати не пізніше 12:31/)).toBeInTheDocument()
    expect(screen.getByText(/C\. Острів, потім генератор — порада/)).toBeInTheDocument()
    expect(screen.getByText(/неможливо: заряду не вистачить/)).toBeInTheDocument()
    expect(screen.getByText(/генератор з 11:43/)).toBeInTheDocument()
    expect(screen.getByText(/\(оцінка\)/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'План і факт за місяць →' }))
    expect(await screen.findByText(/відключень 1 \(поза графіком 0\)/)).toBeInTheDocument()
    expect(screen.getByText(/острів → генератор/)).toBeInTheDocument()
    expect(screen.getByText(/проти плану 437.5 \(7.4 %\)/)).toBeInTheDocument()
  })
})
