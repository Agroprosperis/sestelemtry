import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DispatchEffectResponse, UzePlanResponse } from '../../api'

const api = vi.hoisted(() => ({ fetchUzeDayPlan: vi.fn(), fetchDispatchEffect: vi.fn() }))
vi.mock('../../api', () => api)

import { EconomicsUzeCard } from './EconomicsUzeCard'

const plan = (): UzePlanResponse => ({
  organization_id: 'ze',
  date: '2026-10-06',
  tz: 'Europe/Kyiv',
  available: true,
  soc_start_pct: 40,
  capacity_kwh: 1720,
  power_kw: 864,
  hours: [],
  totals: {
    optimum_uah: 5200,
    fact_uah: 3900,
    reserve_uah: 1300,
    captured_share: 0.75,
    charge_pv_kwh: 900,
    charge_grid_kwh: 400,
    discharge_kwh: 1200,
    export_val_uah: 0,
    load_val_uah: 9000,
    charge_pv_cost_uah: 2000,
    grid_cost_uah: 1100,
    degradation_uah: 720,
  },
})

const effect = (): DispatchEffectResponse => ({
  site_id: 'ze',
  date: '2026-10-07',
  version: 3,
  available: true,
  effect: {
    date: '2026-10-07', from: '2026-10-07T18:00:00Z', until: '2026-10-07T21:00:00Z', hours: 3,
    ess_to_load_uah: 7400, ess_to_grid_uah: 0, pv_charge_cost_uah: 0, grid_charge_cost_uah: 0, degradation_uah: 520,
    flows_uah: 6880, soc_open_pct: 70, soc_close_pct: 25, soc_carry_uah: -4100, shadow_price_uah: 5.3,
    net_effect_uah: 2780, baseline_cost_uah: 9900, plan_cost_uah: 3020,
    ess_to_load_kwh: 860, ess_to_grid_kwh: 0, charge_pv_kwh: 0, charge_grid_kwh: 0,
  },
})

describe('EconomicsUzeCard', () => {
  beforeEach(() => {
    api.fetchUzeDayPlan.mockReset()
    api.fetchDispatchEffect.mockReset()
  })

  it('shows fact, optimum and reserve with the optimum waterfall for a past day', async () => {
    api.fetchUzeDayPlan.mockResolvedValue(plan())
    render(<EconomicsUzeCard organizationID="ze" date="2026-10-06" today="2026-10-07" />)
    expect(await screen.findByText('УЗЕ: факт / оптимум / резерв')).toBeInTheDocument()
    expect(screen.getByText('реалізовано 75%')).toBeInTheDocument()
    // 9000 − 2000 − 1100 − 720 = 5180: the 20 грн gap is the SOC change.
    expect(screen.getByText('Зміна запасу SOC за добу')).toBeInTheDocument()
    expect(api.fetchDispatchEffect).not.toHaveBeenCalled()
  })

  it('shows today so far and the expected effect of the applied plan', async () => {
    api.fetchUzeDayPlan.mockResolvedValue({ ...plan(), date: '2026-10-07' })
    api.fetchDispatchEffect.mockResolvedValue(effect())
    render(<EconomicsUzeCard organizationID="ze" date="2026-10-07" today="2026-10-07" />)
    expect(await screen.findByText('Очікуваний ефект плану УЗЕ')).toBeInTheDocument()
    expect(screen.getByText('УЗЕ: факт / оптимум / резерв')).toBeInTheDocument()
    expect(screen.getByText(/за години, що минули/)).toBeInTheDocument()
    expect(screen.getByText('план пульта · версія 3')).toBeInTheDocument()
    expect(screen.getByText(/21:00–24:00 · 3 год плану/)).toBeInTheDocument()
    expect(screen.getByText(/Запас SOC \(70% → 25%/)).toBeInTheDocument()
  })

  it('skips an empty optimum early today', async () => {
    api.fetchUzeDayPlan.mockResolvedValue({ ...plan(), available: false })
    api.fetchDispatchEffect.mockResolvedValue(effect())
    render(<EconomicsUzeCard organizationID="ze" date="2026-10-07" today="2026-10-07" />)
    expect(await screen.findByText('Очікуваний ефект плану УЗЕ')).toBeInTheDocument()
    expect(screen.queryByText('УЗЕ: факт / оптимум / резерв')).toBeNull()
  })

  it('explains a missing plan', async () => {
    api.fetchDispatchEffect.mockResolvedValue({ site_id: 'ze', date: '2026-10-08', version: 0, available: false, reason: 'План пульта не покриває цю дату.' })
    render(<EconomicsUzeCard organizationID="ze" date="2026-10-08" today="2026-10-07" />)
    expect(await screen.findByText('План пульта не покриває цю дату.')).toBeInTheDocument()
    expect(api.fetchUzeDayPlan).not.toHaveBeenCalled()
  })

  it('stays hidden for a site without an edge device', async () => {
    api.fetchDispatchEffect.mockRejectedValue(new Error('dispatch/effect request failed: 404 — unknown edge site'))
    const { container } = render(<EconomicsUzeCard organizationID="pe" date="2026-10-07" today="2026-10-07" />)
    await waitFor(() => expect(api.fetchDispatchEffect).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })
})
