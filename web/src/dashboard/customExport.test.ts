import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as api from '../api'
import {
  annotateMetricHeader,
  autoBucket,
  customExportFilename,
  fetchCustomExportData,
  rawExportMetricKeys,
  RAW_SAMPLES_LIMIT,
  RAW_SAMPLES_MAX_DAYS,
} from './customExport'

describe('autoBucket', () => {
  it('picks 5-minute resolution for short ranges', () => {
    const from = new Date(2026, 4, 7)
    const to = new Date(2026, 4, 9) // 2 days
    expect(autoBucket(from, to)).toBe('5 minutes')
  })

  it('switches to hourly for ranges over 2 days', () => {
    const from = new Date(2026, 4, 1)
    const to = new Date(2026, 4, 8) // 7 days
    expect(autoBucket(from, to)).toBe('1 hour')
  })

  it('switches to daily for ranges over 35 days', () => {
    const from = new Date(2026, 0, 1)
    const to = new Date(2026, 2, 1) // ~59 days
    expect(autoBucket(from, to)).toBe('1 day')
  })

  it('switches to monthly for ranges over a year', () => {
    const from = new Date(2024, 0, 1)
    const to = new Date(2026, 0, 1) // ~2 years
    expect(autoBucket(from, to)).toBe('1 month')
  })
})

describe('customExportFilename', () => {
  it('renders inclusive end and bucket suffix for analysts to skim', () => {
    expect(
      customExportFilename({
        organizationID: 'pe',
        from: new Date(2026, 4, 1),
        to: new Date(2026, 4, 8), // exclusive
        bucket: '1 hour',
      }),
    ).toBe('export_pe_2026-05-01_2026-05-07_1hour.csv')
  })
})

describe('fetchCustomExportData', () => {
  beforeEach(() => {
    vi.spyOn(api, 'fetchTimeseries').mockImplementation(async (input) => ({
      organization_id: input.organizationID,
      metric_keys: input.metricKeys,
      bucket: input.bucket,
      from: input.from,
      to: input.to,
      points: input.metricKeys.flatMap((mk) => {
        if (mk === 'soc_percent') {
          return [
            { time: '2026-05-07T00:00:00.000Z', metric_key: mk, value: 50 },
            { time: '2026-05-07T01:00:00.000Z', metric_key: mk, value: 60 },
          ]
        }
        if (mk === 'accumulated_pv_energy_yield_kwh') {
          return [
            { time: '2026-05-07T00:00:00.000Z', metric_key: mk, value: 10 },
            { time: '2026-05-07T01:00:00.000Z', metric_key: mk, value: 25 },
          ]
        }
        return [
          { time: '2026-05-07T00:00:00.000Z', metric_key: mk, value: 1 },
          { time: '2026-05-07T01:00:00.000Z', metric_key: mk, value: 2 },
        ]
      }),
    }))
    vi.spyOn(api, 'fetchDAMPrices').mockResolvedValue({
      zone: 2,
      from: '2026-05-07',
      to: '2026-05-07',
      prices: [
        { delivery_date: '2026-05-07', hour: 1, zone: 2, price_uah_per_mwh: 1500 },
        { delivery_date: '2026-05-07', hour: 2, zone: 2, price_uah_per_mwh: 2500 },
      ],
    })
    vi.spyOn(api, 'fetchPvForecast').mockResolvedValue([])
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('returns only the selected columns and skips the network when nothing is checked', async () => {
    const table = await fetchCustomExportData({
      organizationID: 'pe',
      from: new Date(2026, 4, 7),
      to: new Date(2026, 4, 8),
      bucket: '1 hour',
      columns: {
        energy: false,
        flow: false,
        price: false,
        soc: false,
        power: false,
        device: false,
        forecast: false,
      },
    })
    expect(table.rows).toEqual([])
    expect(table.headers).toEqual(['time'])
    expect(api.fetchTimeseries).not.toHaveBeenCalled()
  })

  it('joins energy, soc, and power columns by bucket-start timestamp', async () => {
    const table = await fetchCustomExportData({
      organizationID: 'pe',
      from: new Date(2026, 4, 7),
      to: new Date(2026, 4, 8),
      bucket: '1 hour',
      columns: {
        energy: true,
        flow: false,
        price: false,
        soc: true,
        power: true,
        device: false,
        forecast: false,
      },
    })
    expect(table.headers).toContain('accumulated_pv_energy_yield_kwh')
    expect(table.headers).toContain('soc_percent')
    expect(table.headers).toContain('active_pv_power_kw')
    expect(table.headers).not.toContain('dam_price_uah_per_mwh')
    expect(table.rows.length).toBeGreaterThan(0)
    // Both series share the same point timestamps so they must join onto
    // a single row each, regardless of the host machine's local timezone.
    const first = table.rows[0]
    expect(first.accumulated_pv_energy_yield_kwh).toBe(10)
    expect(first.soc_percent).toBe(50)
    expect(first.active_pv_power_kw).toBe(1)
  })

  it('annotates wide-CSV headers with Modbus register suffix when registers are provided', async () => {
    const table = await fetchCustomExportData({
      organizationID: 'pe',
      from: new Date(2026, 4, 7),
      to: new Date(2026, 4, 8),
      bucket: '1 hour',
      columns: {
        // price is intentionally omitted so the first bucket is one
        // of the timeseries points (not a DAM-only bucket whose
        // telemetry cells would be null in any timezone).
        energy: true,
        flow: false,
        price: false,
        soc: true,
        power: true,
        device: false,
        forecast: false,
      },
      registerAddresses: {
        accumulated_pv_energy_yield_kwh: 40446,
        accumulated_electricity_sold_kwh: 40454,
        accumulated_electricity_purchased_kwh: 40450,
        accumulated_power_consumption_kwh: 40458,
        total_energy_charged_kwh: 40472,
        total_energy_discharged_kwh: 40476,
        soc_percent: 40515,
        active_pv_power_kw: 40388,
        active_ess_power_kw: 40392,
        grid_connected_active_power_kw: 40505,
        load_power_kw: 40503,
      },
    })
    expect(table.headers).toContain('accumulated_pv_energy_yield_kwh_40446')
    expect(table.headers).toContain('soc_percent_40515')
    expect(table.headers).toContain('active_pv_power_kw_40388')
    expect(table.headers).toContain('time')
    // Row keys must agree with the annotated headers so the CSV
    // serializer renders the values in the right cells.
    const first = table.rows[0]
    expect(first['accumulated_pv_energy_yield_kwh_40446']).toBe(10)
    expect(first['soc_percent_40515']).toBe(50)
    expect(first['active_pv_power_kw_40388']).toBe(1)
    expect(first['accumulated_pv_energy_yield_kwh']).toBeUndefined()
  })

  it('skips network calls for unselected columns', async () => {
    await fetchCustomExportData({
      organizationID: 'pe',
      from: new Date(2026, 4, 7),
      to: new Date(2026, 4, 8),
      bucket: '1 hour',
      columns: {
        energy: false,
        flow: false,
        price: false,
        soc: true,
        power: false,
        device: false,
        forecast: false,
      },
    })
    expect(api.fetchTimeseries).toHaveBeenCalledTimes(1)
    expect(api.fetchTimeseries).toHaveBeenCalledWith(
      expect.objectContaining({ metricKeys: ['soc_percent'], aggregation: 'avg' }),
      undefined,
    )
    expect(api.fetchDAMPrices).not.toHaveBeenCalled()
    expect(api.fetchPvForecast).not.toHaveBeenCalled()
  })

  it('fetches local_time_epoch_s with last-aggregation when the device column is checked', async () => {
    const table = await fetchCustomExportData({
      organizationID: 'pe',
      from: new Date(2026, 4, 7),
      to: new Date(2026, 4, 8),
      bucket: '1 hour',
      columns: {
        energy: false,
        flow: false,
        price: false,
        soc: false,
        power: false,
        device: true,
        forecast: false,
      },
      registerAddresses: { local_time_epoch_s: 40009 },
    })
    expect(api.fetchTimeseries).toHaveBeenCalledWith(
      expect.objectContaining({
        metricKeys: ['local_time_epoch_s'],
        aggregation: 'last',
      }),
      undefined,
    )
    expect(table.headers).toContain('local_time_epoch_s_40009')
  })

  it('fetches the four synthetic flow counters with delta aggregation when the flow column is checked', async () => {
    // Flow metrics share the cumulative-kWh shape of the
    // accumulators, so the bucketed export must use the same delta
    // aggregation to render per-bucket production rather than the
    // monotonically growing raw counter.
    const table = await fetchCustomExportData({
      organizationID: 'pe',
      from: new Date(2026, 4, 7),
      to: new Date(2026, 4, 8),
      bucket: '1 hour',
      columns: {
        energy: false,
        flow: true,
        price: false,
        soc: false,
        power: false,
        device: false,
        forecast: false,
      },
    })
    expect(api.fetchTimeseries).toHaveBeenCalledWith(
      expect.objectContaining({
        metricKeys: ['pv_to_ess_kwh', 'grid_to_ess_kwh', 'ess_to_load_kwh', 'ess_to_grid_kwh'],
        aggregation: 'delta',
      }),
      undefined,
    )
    expect(table.headers).toEqual([
      'time',
      'pv_to_ess_kwh',
      'grid_to_ess_kwh',
      'ess_to_load_kwh',
      'ess_to_grid_kwh',
    ])
    // Synthetic columns have no Modbus address, so the headers stay
    // un-suffixed even when a registerAddresses map is supplied for
    // other columns (covered by the energy/soc/power test above).
    expect(table.rows.length).toBeGreaterThan(0)
    expect(table.rows[0].pv_to_ess_kwh).toBe(1)
  })

  it('refuses the raw bucket — that path goes through fetchRawSamplesCsv', async () => {
    await expect(
      fetchCustomExportData({
        organizationID: 'pe',
        from: new Date(2026, 4, 7),
        to: new Date(2026, 4, 8),
        bucket: 'raw',
        columns: {
          energy: true,
          flow: false,
          price: false,
          soc: false,
          power: false,
          device: false,
          forecast: false,
        },
      }),
    ).rejects.toThrow(/raw bucket/)
  })
})

describe('rawExportMetricKeys', () => {
  it('flattens column groups into the metric_keys list /api/v1/samples expects', () => {
    const keys = rawExportMetricKeys({
      energy: true,
      flow: true,
      price: true, // ignored — DAM prices have no raw rows
      soc: true,
      power: true,
      device: true,
      forecast: true, // ignored — n8n forecast has no raw rows
    })
    expect(keys).toContain('soc_percent')
    expect(keys).toContain('accumulated_pv_energy_yield_kwh')
    expect(keys).toContain('active_pv_power_kw')
    expect(keys).toContain('local_time_epoch_s')
    // Synthetic flow counters live in telemetry_samples (written by
    // the collector's energyflow package), so they belong in the raw
    // export's metric_keys list alongside the catalog metrics.
    expect(keys).toContain('pv_to_ess_kwh')
    expect(keys).toContain('grid_to_ess_kwh')
    expect(keys).toContain('ess_to_load_kwh')
    expect(keys).toContain('ess_to_grid_kwh')
    // Forecast/price metric keys must not leak into the request — the
    // server would reject them since they don't live in
    // telemetry_samples, and we'd burn a round trip to find out.
    expect(keys).not.toContain('dam_price_uah_per_mwh')
    expect(keys).not.toContain('planned_ac_kw_forecast')
  })

  it('returns an empty array when no telemetry column is selected', () => {
    expect(
      rawExportMetricKeys({
        energy: false,
        flow: false,
        price: true,
        soc: false,
        power: false,
        device: false,
        forecast: true,
      }),
    ).toEqual([])
  })

  it('auto-includes local_time_epoch_s alongside any selected telemetry metric', () => {
    // The synthetic local_time column in the wide CSV is derived
    // from this register; without it the column would always be
    // empty regardless of how the pivot reshapes the rows.
    const keys = rawExportMetricKeys({
      energy: false,
      flow: false,
      price: false,
      soc: true,
      power: false,
      device: false,
      forecast: false,
    })
    expect(keys).toContain('soc_percent')
    expect(keys).toContain('local_time_epoch_s')
  })

  it('does not duplicate local_time_epoch_s when the device group is also selected', () => {
    const keys = rawExportMetricKeys({
      energy: false,
      flow: false,
      price: false,
      soc: false,
      power: false,
      device: true,
      forecast: false,
    })
    const occurrences = keys.filter((k) => k === 'local_time_epoch_s').length
    expect(occurrences).toBe(1)
  })

  it('emits only the four flow counters (plus local_time) when the flow group is the lone selection', () => {
    // Operators investigating an allocation regression want a CSV of
    // just the energy-flow rows — verify nothing else sneaks in.
    const keys = rawExportMetricKeys({
      energy: false,
      flow: true,
      price: false,
      soc: false,
      power: false,
      device: false,
      forecast: false,
    })
    expect(keys).toEqual([
      'pv_to_ess_kwh',
      'grid_to_ess_kwh',
      'ess_to_load_kwh',
      'ess_to_grid_kwh',
      'local_time_epoch_s',
    ])
  })
})

describe('raw export limits', () => {
  it('exposes server-aligned constants so the dialog hint stays truthful', () => {
    expect(RAW_SAMPLES_LIMIT).toBe(5_000_000)
    expect(RAW_SAMPLES_MAX_DAYS).toBe(31)
  })
})

describe('annotateMetricHeader', () => {
  it('appends `_<address>` for metrics with a known Modbus register', () => {
    expect(
      annotateMetricHeader('active_pv_power_kw', { active_pv_power_kw: 40388 }),
    ).toBe('active_pv_power_kw_40388')
  })

  it('returns the plain key when no addresses map is supplied', () => {
    expect(annotateMetricHeader('active_pv_power_kw')).toBe('active_pv_power_kw')
  })

  it('returns the plain key for metrics absent from the addresses map (synthetic columns)', () => {
    expect(
      annotateMetricHeader('dam_price_uah_per_mwh', { active_pv_power_kw: 40388 }),
    ).toBe('dam_price_uah_per_mwh')
  })
})
