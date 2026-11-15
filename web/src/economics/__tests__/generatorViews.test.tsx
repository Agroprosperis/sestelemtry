import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { EconomicsAnnualResponse, EconomicsHourApi } from '../../api'
import fixture from '../annual/__fixtures_annual_ze.json'
import { EconomicsTable } from '../components/EconomicsTable'
import { EconomicsTopSection } from '../components/EconomicsTopSection'
import { mapHourRow } from '../useEconomicsData'

afterEach(cleanup)

const data = fixture as unknown as EconomicsAnnualResponse

describe('generator in the period cards', () => {
  it('adds the «Генератор» section once the period has generator data', () => {
    render(
      <EconomicsTopSection
        totals={{
          ...data.totals,
          gen_kwh: 518,
          gen_to_load_kwh: 517,
          gen_to_ess_kwh: 0.8,
          load_offgrid_kwh: 940,
          gen_fuel_cost_uah: 12821,
          gen_fuel_l: 155,
          gen_fuel_method: 'per_kwh',
          effect_offgrid_uah: 6359,
          minutes_generator: 90,
          minutes_island: 88,
        }}
        scope="month"
      />,
    )
    const section = screen.getByText('Генератор', { selector: 'h3' }).closest('.eco-kpi-section') as HTMLElement
    expect(within(section).getByText(/У роботі: 1 год 30 хв/)).toBeInTheDocument()
    expect(within(section).getByText(/155 л · за кВт·год/)).toBeInTheDocument()
    expect(within(section).getByText('2 год 58 хв')).toBeInTheDocument()
    expect(within(section).getByText('Ефект годин без мережі')).toBeInTheDocument()
  })

  it('stays as before without generator data', () => {
    render(<EconomicsTopSection totals={data.totals} scope="month" />)
    expect(screen.queryByText('Генератор', { selector: 'h3' })).toBeNull()
  })
})

function hour(h: number, fields: Partial<EconomicsHourApi> = {}): EconomicsHourApi {
  return {
    hour: h,
    hour_start: `2026-11-15T${String(h).padStart(2, '0')}:00:00+02:00`,
    rdn_uah_per_kwh: 6,
    pv_kwh: 0,
    grid_import_kwh: 0,
    grid_export_kwh: 0,
    ess_charged_kwh: 0,
    ess_discharged_kwh: 0,
    pv_to_ess_kwh: 0,
    grid_to_ess_kwh: 0,
    ess_to_load_kwh: 0,
    ess_to_grid_kwh: 0,
    load_kwh: 0,
    pv_to_load_kwh: 0,
    pv_to_grid_kwh: 0,
    grid_to_load_kwh: 0,
    import_price_uah_per_kwh: 11,
    export_price_uah_per_kwh: 6,
    baseline_cost_uah: 0,
    actual_cost_uah: 0,
    effect_uah: 0,
    ess_net_uah: 0,
    ess_remaining_kwh_start: null,
    ess_cost_basis_uah_start: null,
    ess_avg_cost_uah_per_kwh_start: null,
    ess_withdrawn_cost_uah: null,
    ess_realized_profit_uah: null,
    ess_cost_basis_uah_end: null,
    ess_avg_cost_uah_per_kwh_end: null,
    ess_residual_kwh_end: null,
    ...fields,
  }
}

describe('generator in the hourly table', () => {
  it('shows the generator group and its charge of the battery on a generator day', () => {
    const rows = Array.from({ length: 24 }, (_, h) => mapHourRow(hour(h)))
    rows[23] = mapHourRow(
      hour(23, {
        ess_charged_kwh: 0.2,
        load_kwh: 341.8,
        baseline_cost_uah: 8460,
        actual_cost_uah: 8465,
        effect_uah: -5,
        ess_net_uah: -5,
        gen_kwh: 342,
        minutes_generator: 60,
        gen_accounted: true,
        gen_to_load_kwh: 341.8,
        gen_to_ess_kwh: 0.2,
        load_offgrid_kwh: 341.8,
        gen_fuel_cost_uah: 8465,
        gen_fuel_l: 102.6,
        gen_fuel_method: 'per_kwh',
        gen_charge_cost_uah: 5,
      }),
    )
    render(<EconomicsTable rows={rows} organizationID="ke" date="2026-11-15" />)
    expect(screen.getByText('Генератор (виробіток)')).toBeInTheDocument()
    expect(screen.getByText('Робота генератора')).toBeInTheDocument()
    expect(screen.getByText('Витрати: Заряд УЗЕ від генератора')).toBeInTheDocument()
    expect(screen.getAllByText('Генератор → Споживання')).toHaveLength(2)
  })

  it('keeps a day without generator data as before', () => {
    const rows = Array.from({ length: 24 }, (_, h) => mapHourRow(hour(h)))
    render(<EconomicsTable rows={rows} organizationID="ke" date="2026-11-15" />)
    expect(screen.queryByText('Генератор (виробіток)')).toBeNull()
    expect(screen.queryByText('Витрати: Заряд УЗЕ від генератора')).toBeNull()
  })
})
