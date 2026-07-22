import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchPlantInventory } from '../api'
import type { PlantInventory } from '../types'

describe('fetchPlantInventory', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('returns the snapshot on 200', async () => {
    const body: PlantInventory = {
      organization_id: 'ab',
      time: '2026-07-22T10:00:00Z',
      poll_reason: 'startup',
      pv_rated_kw: 450,
      ess_rated_kw: 864,
      ess_rated_kwh: 1720,
      ess_count: 8,
      pcs_count: 8,
      ess_soh_pct: 99.5,
      active_power_control_mode: 4,
      quality_flags: [],
    }
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify(body), { status: 200 })),
    )
    const got = await fetchPlantInventory('ab')
    expect(got?.pv_rated_kw).toBe(450)
    expect(got?.organization_id).toBe('ab')
  })

  it('returns null on 404', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('no plant inventory snapshot', { status: 404 })),
    )
    const got = await fetchPlantInventory('ab')
    expect(got).toBeNull()
  })

  it('throws on other errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('boom', { status: 500 })),
    )
    await expect(fetchPlantInventory('ab')).rejects.toThrow(/500/)
  })
})
