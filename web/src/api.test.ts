import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchRawSamplesCsv } from './api'

// fetchRawSamplesCsv post-processes the response body just enough to
// detect the server's truncation sentinel and pull the suggested
// filename out of Content-Disposition. These tests exercise both
// happy paths (no truncation, no sentinel) and the truncation path so
// the dialog's "обмежено N рядками" warning fires only when it should.

describe('fetchRawSamplesCsv', () => {
  const baseHeaders = new Headers({
    'content-type': 'text/csv; charset=utf-8',
    'content-disposition': 'attachment; filename="samples_pe_20260509T000000Z_20260510T000000Z.csv"',
  })

  function mockFetch(body: string, status = 200, headers = baseHeaders) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(body, { status, headers })),
    )
  }

  beforeEach(() => {
    vi.unstubAllGlobals()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('parses the streamed CSV body and reports the row count', async () => {
    // The server emits a leading BOM so Excel opens Cyrillic correctly.
    // `Response.text()` strips it during UTF-8 decode, which is what
    // the real browser does too — the test mirrors that.
    const body =
      'time,metric_key,value,labels\r\n' +
      '2026-05-09T10:00:00Z,active_pv_power_kw,12.345,\r\n' +
      '2026-05-09T10:00:01Z,soc_percent,86.5,"{""unit_id"":""ess-1""}"\r\n'
    mockFetch(body)

    const result = await fetchRawSamplesCsv({
      organizationID: 'pe',
      metricKeys: ['active_pv_power_kw', 'soc_percent'],
      from: '2026-05-09T00:00:00Z',
      to: '2026-05-10T00:00:00Z',
    })
    expect(result.text).toBe(body)
    expect(result.filename).toBe('samples_pe_20260509T000000Z_20260510T000000Z.csv')
    expect(result.rows).toBe(2)
    expect(result.truncated).toBe(false)
  })

  it('detects the __TRUNCATED__ sentinel and excludes it from the row count', async () => {
    const body =
      'time,metric_key,value,labels\r\n' +
      '2026-05-09T10:00:00Z,soc_percent,80,\r\n' +
      '2026-05-09T10:00:01Z,soc_percent,81,\r\n' +
      '__TRUNCATED__,,2,"{""reason"":""row_limit"",""limit"":2}"\r\n'
    mockFetch(body)

    const result = await fetchRawSamplesCsv({
      organizationID: 'pe',
      metricKeys: ['soc_percent'],
      from: '2026-05-09T00:00:00Z',
      to: '2026-05-10T00:00:00Z',
      limit: 2,
    })
    expect(result.truncated).toBe(true)
    expect(result.rows).toBe(2) // sentinel is not counted as a data row
  })

  it('handles an empty result (header only) without underflowing the row count', async () => {
    const body = 'time,metric_key,value,labels\r\n'
    mockFetch(body)

    const result = await fetchRawSamplesCsv({
      organizationID: 'pe',
      metricKeys: ['soc_percent'],
      from: '2026-05-09T00:00:00Z',
      to: '2026-05-10T00:00:00Z',
    })
    expect(result.rows).toBe(0)
    expect(result.truncated).toBe(false)
  })

  it('falls back to a generic filename when the server omits Content-Disposition', async () => {
    const body = 'time,metric_key,value,labels\r\n'
    mockFetch(body, 200, new Headers({ 'content-type': 'text/csv' }))

    const result = await fetchRawSamplesCsv({
      organizationID: 'pe',
      metricKeys: ['soc_percent'],
      from: '2026-05-09T00:00:00Z',
      to: '2026-05-10T00:00:00Z',
    })
    expect(result.filename).toBe('samples.csv')
  })

  it('surfaces non-200 responses as a thrown Error so the dialog renders the body in red', async () => {
    mockFetch('range must be <= 744h0m0s\n', 400, new Headers({ 'content-type': 'text/plain' }))

    await expect(
      fetchRawSamplesCsv({
        organizationID: 'pe',
        metricKeys: ['soc_percent'],
        from: '2026-01-01T00:00:00Z',
        to: '2026-03-01T00:00:00Z',
      }),
    ).rejects.toThrow(/samples request failed: 400 range must be <= 744h0m0s/)
  })
})
