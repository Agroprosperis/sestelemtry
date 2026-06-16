import { describe, expect, it } from 'vitest'
import { formatEpochSecondsLocal, pivotRawCsvToWide } from './pivotRaw'

// pivotRawCsvToWide reshapes /api/v1/samples long output into the
// "one row per timestamp" layout an analyst expects from a
// spreadsheet export. These tests lock the contract: column order,
// per-device grouping, header annotation, truncation-sentinel
// passthrough, and the synthetic device_type / local_time columns.

const HEADER = 'time,metric_key,modbus_register,data_type,gain,value,labels\r\n'

describe('pivotRawCsvToWide', () => {
  it('groups same-timestamp samples from one device into a single wide row', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101"",""device_type"":""smartlogger""}"\r\n' +
      '2026-05-09T13:00:00+03:00,load_power_kw,40503,UINT32,0.001,197.68,"{""device_host"":""10.28.40.101"",""device_type"":""smartlogger""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['active_pv_power_kw', 'load_power_kw'],
    })
    expect(out.rows).toBe(1)
    expect(out.csv).toContain(
      'time,device_type,device_host,active_pv_power_kw,load_power_kw',
    )
    expect(out.csv).toContain(
      '2026-05-09T13:00:00+03:00,smartlogger,10.28.40.101,97.12,197.68',
    )
  })

  it('keeps two devices polled at the same instant on separate rows', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,active_ess_power_kw,40392,INT32,0.001,-0.82,"{""device_host"":""10.28.40.102"",""device_type"":""smartlogger""}"\r\n' +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101"",""device_type"":""smartlogger""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['active_pv_power_kw', 'active_ess_power_kw'],
    })
    expect(out.rows).toBe(2)
    // Each device retains its own row even though the timestamp is
    // identical — the device_host label is what disambiguates them.
    expect(out.csv).toContain(',smartlogger,10.28.40.101,97.12,')
    expect(out.csv).toContain(',smartlogger,10.28.40.102,,-0.82')
  })

  it('disambiguates two devices of different vendor types at the same timestamp', () => {
    // device_type is part of the row key, so a sungrow logger and a
    // smartlogger sharing the same poll instant must NOT be merged
    // into a single wide row even when their hosts happen to clash.
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,50.0,"{""device_host"":""10.0.0.1"",""device_type"":""sungrow_logger""}"\r\n' +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,12.5,"{""device_host"":""10.0.0.1"",""device_type"":""smartlogger""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['active_pv_power_kw'],
    })
    expect(out.rows).toBe(2)
    expect(out.csv).toContain(',sungrow_logger,10.0.0.1,50')
    expect(out.csv).toContain(',smartlogger,10.0.0.1,12.5')
  })

  it('annotates wide-CSV headers with Modbus addresses when supplied', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101"",""device_type"":""smartlogger""}"\r\n'
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
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101"",""device_type"":""smartlogger""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      // soc_percent was requested but never sampled; wide CSV must
      // still keep its column so the output schema matches the
      // request and downstream pivot tables don't shift.
      metricKeys: ['active_pv_power_kw', 'soc_percent'],
    })
    expect(out.csv).toContain('time,device_type,device_host,active_pv_power_kw,soc_percent')
    expect(out.csv).toContain('2026-05-09T13:00:00+03:00,smartlogger,10.28.40.101,97.12,\r\n')
  })

  it('falls back to the default device_type when the label is absent', () => {
    // The collector doesn't currently stamp device_type on samples
    // (labels carry only site_id / device_id / device_host), so the
    // wide CSV synthesizes the column from the project's only
    // shipping catalog — Huawei SmartLogger. If a future collector
    // change starts emitting an explicit device_type label, that
    // value takes over (covered by the dedicated test above).
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,soc_percent,40515,UINT16,0.1,86.5,"{""device_host"":""10.28.40.101""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['soc_percent'],
    })
    expect(out.csv).toContain('2026-05-09T13:00:00+03:00,smartlogger,10.28.40.101,86.5')
  })

  it('decodes the SmartLogger local_time_epoch_s register into a calendar local_time column', () => {
    // 1715252400 = 2024-05-09 11:00:00 UTC (the number is the wall
    // clock the device claims, regardless of the analyst's TZ). The
    // export treats the integer as if it was stamped in the device's
    // own timezone and renders it byte-for-byte.
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,local_time_epoch_s,40009,UINT32,1,1715252400,"{""device_host"":""10.28.40.101"",""device_type"":""smartlogger""}"\r\n' +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,97.12,"{""device_host"":""10.28.40.101"",""device_type"":""smartlogger""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['local_time_epoch_s', 'active_pv_power_kw'],
    })
    // local_time slots between device_host and the metric columns so
    // the analyst sees the decoded clock right next to the device
    // identity, ahead of any vendor metrics.
    expect(out.csv).toContain(
      'time,device_type,device_host,local_time,local_time_epoch_s,active_pv_power_kw',
    )
    expect(out.csv).toContain(
      '2026-05-09T13:00:00+03:00,smartlogger,10.28.40.101,2024-05-09 11:00:00,1715252400,97.12',
    )
  })

  it('omits the local_time column entirely when local_time_epoch_s was not requested', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,soc_percent,40515,UINT16,0.1,86.5,"{""device_host"":""10.28.40.101"",""device_type"":""smartlogger""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['soc_percent'],
    })
    expect(out.csv.split('\r\n')[0]).not.toContain('local_time')
  })

  it('detects the truncation sentinel but strips it from the wide CSV body', () => {
    const long =
      HEADER +
      '2026-05-09T13:00:00+03:00,soc_percent,40515,UINT16,0.1,80,"{""device_host"":""10.28.40.102"",""device_type"":""smartlogger""}"\r\n' +
      '__TRUNCATED__,,,,,1,"{""reason"":""row_limit"",""limit"":1}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['soc_percent'],
    })
    expect(out.truncated).toBe(true)
    // Sentinel is intentionally absent from the wide body — its
    // 7-column long-format shape would corrupt (time, device_type,
    // device_host, m1..mN). The dialog signals truncation via the
    // boolean flag (and the HTTP-level detection in fetchRawSamplesCsv)
    // instead.
    expect(out.csv).not.toMatch(/__TRUNCATED__/)
  })

  it('groups metric-major input (non-contiguous polls) and re-sorts the wide rows by time', () => {
    // The server streams all of metric A (time-ordered), then all of
    // metric B — so samples from one poll are scattered. The pivot must
    // still merge them per timestamp and emit chronological rows.
    const long =
      HEADER +
      '2026-05-09T13:00:01+03:00,active_pv_power_kw,40388,UINT32,0.001,11,"{""device_host"":""10.0.0.1""}"\r\n' +
      '2026-05-09T13:00:00+03:00,active_pv_power_kw,40388,UINT32,0.001,10,"{""device_host"":""10.0.0.1""}"\r\n' +
      '2026-05-09T13:00:01+03:00,load_power_kw,40503,UINT32,0.001,21,"{""device_host"":""10.0.0.1""}"\r\n' +
      '2026-05-09T13:00:00+03:00,load_power_kw,40503,UINT32,0.001,20,"{""device_host"":""10.0.0.1""}"\r\n'
    const out = pivotRawCsvToWide({
      longCsv: long,
      metricKeys: ['active_pv_power_kw', 'load_power_kw'],
    })
    expect(out.rows).toBe(2)
    const lines = out.csv.split('\r\n')
    // Two distinct polls merged into one wide row each, in time order.
    expect(lines[1]).toBe('2026-05-09T13:00:00+03:00,smartlogger,10.0.0.1,10,20')
    expect(lines[2]).toBe('2026-05-09T13:00:01+03:00,smartlogger,10.0.0.1,11,21')
  })

  it('handles header-only response (no samples) without crashing', () => {
    const out = pivotRawCsvToWide({
      longCsv: HEADER,
      metricKeys: ['soc_percent'],
    })
    expect(out.rows).toBe(0)
    expect(out.truncated).toBe(false)
    expect(out.csv.startsWith('time,device_type,device_host,soc_percent')).toBe(true)
  })
})

describe('formatEpochSecondsLocal', () => {
  it('renders epoch seconds as "YYYY-MM-DD HH:MM:SS" using the device-local clock', () => {
    expect(formatEpochSecondsLocal('1715252400')).toBe('2024-05-09 11:00:00')
  })

  it('returns empty for non-numeric or empty input so a bad sample does not poison the column', () => {
    expect(formatEpochSecondsLocal('')).toBe('')
    expect(formatEpochSecondsLocal('   ')).toBe('')
    expect(formatEpochSecondsLocal('not-a-number')).toBe('')
    expect(formatEpochSecondsLocal('NaN')).toBe('')
  })

  it('survives whitespace around the integer', () => {
    expect(formatEpochSecondsLocal('  1715252400  ')).toBe('2024-05-09 11:00:00')
  })
})
