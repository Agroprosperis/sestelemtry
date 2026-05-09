import { describe, expect, it } from 'vitest'
import { pivotRawCsvToWide } from './pivotRaw'

// pivotRawCsvToWide reshapes /api/v1/samples long output into the
// "one row per timestamp" layout an analyst expects from a
// spreadsheet export. These tests lock the contract: column order,
// per-device grouping, header annotation, and truncation-sentinel
// passthrough.

const HEADER = 'time,metric_key,modbus_register,data_type,gain,value,labels\r\n'

describe('pivotRawCsvToWide', () => {
  it('groups same-timestamp samples from one device into a single wide row', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101""}"\r\n' +
      '2026-05-09T13:00:00+03:00,load_power_kw,40503,UINT32,0.001,197.68,"{""device_host"":""10.28.40.101""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['active_pv_power_kw', 'load_power_kw'],
    })
    expect(out.rows).toBe(1)
    expect(out.csv).toContain(
      'time,device_host,active_pv_power_kw,load_power_kw',
    )
    expect(out.csv).toContain(
      '2026-05-09T13:00:00+03:00,10.28.40.101,97.12,197.68',
    )
  })

  it('keeps two devices polled at the same instant on separate rows', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,active_ess_power_kw,40392,INT32,0.001,-0.82,"{""device_host"":""10.28.40.102""}"\r\n' +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['active_pv_power_kw', 'active_ess_power_kw'],
    })
    expect(out.rows).toBe(2)
    // Each device retains its own row even though the timestamp is
    // identical — the device_host label is what disambiguates them.
    expect(out.csv).toContain(',10.28.40.101,97.12,')
    expect(out.csv).toContain(',10.28.40.102,,-0.82')
  })

  it('annotates wide-CSV headers with Modbus addresses when supplied', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['active_pv_power_kw'],
      registerAddresses: { active_pv_power_kw: 40388 },
    })
    expect(out.csv).toContain('active_pv_power_kw_40388')
    // Original (unannotated) header must not also appear — that
    // would duplicate the column and confuse spreadsheet auto-fits.
    expect(out.csv.split('\r\n')[0]).not.toContain(',active_pv_power_kw,')
  })

  it('emits empty cells for metrics absent from the response without dropping their column', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      // soc_percent was requested but never sampled; wide CSV must
      // still keep its column so the output schema matches the
      // request and downstream pivot tables don't shift.
      metricKeys: ['active_pv_power_kw', 'soc_percent'],
    })
    expect(out.csv).toContain('time,device_host,active_pv_power_kw,soc_percent')
    expect(out.csv).toContain('2026-05-09T13:00:00+03:00,10.28.40.101,97.12,\r\n')
  })

  it('passes the truncation sentinel through unchanged so the dialog can still warn', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,soc_percent,40515,UINT16,0.1,80,"{""device_host"":""10.28.40.102""}"\r\n' +
      '__TRUNCATED__,,,,,1,"{""reason"":""row_limit"",""limit"":1}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['soc_percent'],
    })
    expect(out.truncated).toBe(true)
    expect(out.csv).toMatch(/__TRUNCATED__,,,,,1,/)
  })

  it('handles header-only response (no samples) without crashing', () => {
    const out = pivotRawCsvToWide({
      longCsv: HEADER,
      metricKeys: ['soc_percent'],
    })
    expect(out.rows).toBe(0)
    expect(out.truncated).toBe(false)
    expect(out.csv.startsWith('time,device_host,soc_percent')).toBe(true)
  })
})
