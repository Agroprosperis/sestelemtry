import { describe, expect, it } from 'vitest'
import { buildEnergyExport, buildRevenueExport, csvFilename } from './exports'

describe('buildEnergyExport / day preset', () => {
  it('joins power, soc, dam (hourly), and forecast (hourly) onto each 5-min row', () => {
    const { headers, rows } = buildEnergyExport({
      preset: 'day',
      energySeries: [],
      powerSeries: [
        {
          time: '00:00',
          active_pv_power_kw: 0,
          active_ess_power_kw: -1.2,
          grid_connected_active_power_kw: 5,
          load_power_kw: 3.1,
        },
        {
          time: '00:30',
          active_pv_power_kw: 10,
          active_ess_power_kw: 2,
          grid_connected_active_power_kw: -8,
          load_power_kw: 4,
        },
      ],
      damSeries: [
        { time: '00:00', bucketStart: 0, price: 1500 },
        { time: '00:05', bucketStart: 0, price: 1500 },
      ],
      socSeries: [
        { time: '00:00', soc: 42.5 },
        { time: '00:30', soc: null },
      ],
      pvForecastSeries: [{ hour: 0, plannedKw: 7.7 }],
    })
    expect(headers).toEqual([
      'time',
      'active_pv_power_kw',
      'active_ess_power_kw',
      'grid_connected_active_power_kw',
      'load_power_kw',
      'soc_percent',
      'dam_price_uah_per_mwh',
      'planned_ac_kw_forecast',
    ])
    expect(rows).toEqual([
      {
        time: '00:00',
        active_pv_power_kw: 0,
        active_ess_power_kw: -1.2,
        grid_connected_active_power_kw: 5,
        load_power_kw: 3.1,
        soc_percent: 42.5,
        dam_price_uah_per_mwh: 1500,
        planned_ac_kw_forecast: 7.7,
      },
      {
        time: '00:30',
        active_pv_power_kw: 10,
        active_ess_power_kw: 2,
        grid_connected_active_power_kw: -8,
        load_power_kw: 4,
        soc_percent: null,
        dam_price_uah_per_mwh: 1500,
        planned_ac_kw_forecast: 7.7,
      },
    ])
  })

  it('emits null fields when a series is missing entirely', () => {
    const { rows } = buildEnergyExport({
      preset: 'day',
      energySeries: [],
      powerSeries: [
        {
          time: '14:00',
          active_pv_power_kw: 100,
          active_ess_power_kw: null,
          grid_connected_active_power_kw: null,
          load_power_kw: null,
        },
      ],
    })
    expect(rows).toEqual([
      {
        time: '14:00',
        active_pv_power_kw: 100,
        active_ess_power_kw: null,
        grid_connected_active_power_kw: null,
        load_power_kw: null,
        soc_percent: null,
        dam_price_uah_per_mwh: null,
        planned_ac_kw_forecast: null,
      },
    ])
  })
})

describe('buildEnergyExport / month preset', () => {
  it('projects every metric column from the bucket-delta rows in a deterministic order', () => {
    const { headers, rows } = buildEnergyExport({
      preset: 'month',
      energySeries: [
        {
          time: '01',
          accumulated_pv_energy_yield_kwh: 50,
          total_energy_charged_kwh: 10,
        } as never,
        {
          time: '02',
          accumulated_pv_energy_yield_kwh: 60,
          accumulated_electricity_sold_kwh: 5,
        } as never,
      ],
    })
    expect(headers).toEqual([
      'time',
      'accumulated_electricity_sold_kwh',
      'accumulated_pv_energy_yield_kwh',
      'total_energy_charged_kwh',
    ])
    expect(rows).toEqual([
      {
        time: '01',
        accumulated_electricity_sold_kwh: null,
        accumulated_pv_energy_yield_kwh: 50,
        total_energy_charged_kwh: 10,
      },
      {
        time: '02',
        accumulated_electricity_sold_kwh: 5,
        accumulated_pv_energy_yield_kwh: 60,
        total_energy_charged_kwh: null,
      },
    ])
  })
})

describe('buildRevenueExport', () => {
  it('emits one row per timeline bucket with the revenue estimate', () => {
    const { headers, rows } = buildRevenueExport({
      series: [
        { time: '00:00', revenue: null },
        { time: '12:00', revenue: 1234.56 },
      ],
    })
    expect(headers).toEqual(['time', 'revenue_uah'])
    expect(rows).toEqual([
      { time: '00:00', revenue_uah: null },
      { time: '12:00', revenue_uah: 1234.56 },
    ])
  })
})

describe('csvFilename', () => {
  it('produces deterministic names with sanitized organization ids', () => {
    expect(
      csvFilename({
        chart: 'energy',
        organizationID: 'pe',
        preset: 'day',
        anchor: new Date(2026, 4, 7),
      }),
    ).toBe('energy_pe_day_2026-05-07.csv')
    expect(
      csvFilename({
        chart: 'revenue',
        organizationID: 'demo org/test',
        preset: 'month',
        anchor: new Date(2026, 0, 1),
      }),
    ).toBe('revenue_demo_org_test_month_2026-01-01.csv')
  })
})
