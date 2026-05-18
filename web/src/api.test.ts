import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  fetchRawSamplesCsv,
  fetchRegisters,
  fetchWeatherForecastFromAPI,
  resetRegistersCache,
} from './api'

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

  it('passes the supplied tz through to the server query string', async () => {
    let calledUrl = ''
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        calledUrl = url
        return new Response('time,metric_key,modbus_register,data_type,gain,value,labels\r\n', {
          status: 200,
          headers: baseHeaders,
        })
      }),
    )

    await fetchRawSamplesCsv({
      organizationID: 'pe',
      metricKeys: ['soc_percent'],
      from: '2026-05-09T00:00:00Z',
      to: '2026-05-10T00:00:00Z',
      tz: 'Europe/Kyiv',
    })
    expect(calledUrl).toContain('tz=Europe%2FKyiv')
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

describe('fetchRegisters', () => {
  beforeEach(() => {
    resetRegistersCache()
    vi.unstubAllGlobals()
  })

  afterEach(() => {
    resetRegistersCache()
    vi.unstubAllGlobals()
  })

  it('memoizes the response so repeated calls hit /api/v1/registers exactly once', async () => {
    const body = JSON.stringify({
      metadata: {
        active_pv_power_kw: { address: 40388, data_type: 'UINT32', gain: 0.001 },
        soc_percent: { address: 40515, data_type: 'UINT16', gain: 0.1 },
      },
    })
    const fetchSpy = vi.fn(
      async () =>
        new Response(body, {
          status: 200,
          headers: new Headers({ 'content-type': 'application/json' }),
        }),
    )
    vi.stubGlobal('fetch', fetchSpy)

    const a = await fetchRegisters()
    const b = await fetchRegisters()
    expect(fetchSpy).toHaveBeenCalledTimes(1)
    expect(a).toBe(b)
    expect(a.metadata.active_pv_power_kw.address).toBe(40388)
  })

  it('drops the cache on failure so a transient error doesn\'t poison subsequent exports', async () => {
    const fetchSpy = vi
      .fn()
      .mockResolvedValueOnce(new Response('boom', { status: 500 }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ metadata: {} }), {
          status: 200,
          headers: new Headers({ 'content-type': 'application/json' }),
        }),
      )
    vi.stubGlobal('fetch', fetchSpy)

    await expect(fetchRegisters()).rejects.toThrow(/registers request failed: 500/)
    const ok = await fetchRegisters()
    expect(ok.metadata).toEqual({})
    expect(fetchSpy).toHaveBeenCalledTimes(2)
  })
})

describe('fetchWeatherForecastFromAPI', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('returns null when the backend has no rows so the caller falls back to Open-Meteo', async () => {
    const body = JSON.stringify({
      organization_id: 'ze',
      from: '2026-05-15T00:00:00Z',
      to: '2026-05-17T00:00:00Z',
      hourly: [],
      daily: [],
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(body, { status: 200, headers: { 'content-type': 'application/json' } })),
    )
    const got = await fetchWeatherForecastFromAPI({
      organizationID: 'ze',
      from: '2026-05-15',
      to: '2026-05-17',
    })
    expect(got).toBeNull()
  })

  it('throws on non-200 so the caller falls back to Open-Meteo', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('boom', { status: 500 })),
    )
    await expect(
      fetchWeatherForecastFromAPI({ organizationID: 'ze', from: '2026-05-15', to: '2026-05-17' }),
    ).rejects.toThrow(/weather-forecast request failed: 500/)
  })
})
