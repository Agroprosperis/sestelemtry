import { describe, expect, it } from 'vitest'
import type { TimeseriesPoint } from '../../types'
import { daySupplyModes, supplyModeOf, supplySegments, type DaySupplyBucket } from './supply'

describe('supplyModeOf', () => {
  const base = { gridBits: 0, genBits: 0, genKw: 0, busV: 0 }

  it('follows the §4.1 table', () => {
    expect(supplyModeOf({ ...base, gridBits: 1 })).toBe('grid')
    expect(supplyModeOf({ ...base, genBits: 1 })).toBe('generator')
    expect(supplyModeOf({ ...base, genKw: 40 })).toBe('generator')
    expect(supplyModeOf({ ...base, gridBits: 1, genBits: 1 })).toBe('transition')
    expect(supplyModeOf({ ...base, busV: 230 })).toBe('island')
    expect(supplyModeOf(base)).toBe('blackout')
  })

  it('does not count a generator below 5 kW as running', () => {
    expect(supplyModeOf({ ...base, genKw: 4, busV: 230 })).toBe('island')
  })

  it('is unknown without a breaker reading', () => {
    expect(supplyModeOf({ ...base, gridBits: null })).toBe('unknown')
    expect(supplyModeOf({ ...base, genBits: null })).toBe('unknown')
  })
})

function at(hh: number, mm: number): string {
  return new Date(2026, 10, 15, hh, mm).toISOString()
}

function point(metric_key: string, time: string, value: number): TimeseriesPoint {
  return { metric_key, time, value }
}

describe('daySupplyModes', () => {
  const anchor = new Date(2026, 10, 15, 12, 0)
  const now = new Date(2026, 10, 16, 0, 0)

  it('is null for a site without an Encombi', () => {
    expect(daySupplyModes([point('active_pv_power_kw', at(10, 0), 5)], anchor, now)).toBeNull()
  })

  it('reads each bucket from its last readings and leaves the future empty', () => {
    const points = [
      point('grid_breakers_on_bits', at(10, 0), 1),
      point('gen_breakers_on_bits', at(10, 0), 0),
      point('grid_breakers_on_bits', at(10, 5), 0),
      point('gen_breakers_on_bits', at(10, 5), 1),
      point('active_gen_power_kw', at(10, 5), 80),
      point('grid_breakers_on_bits', at(10, 10), 0),
      point('gen_breakers_on_bits', at(10, 10), 0),
      point('gen_bus_voltage_a_v', at(10, 10), 231),
      point('grid_breakers_on_bits', at(10, 20), 1),
      point('gen_breakers_on_bits', at(10, 20), 0),
    ]
    const buckets = daySupplyModes(points, anchor, new Date(2026, 10, 15, 10, 27))!
    const mode = (time: string) => buckets.find((b) => b.time === time)?.mode
    expect(mode('10:00')).toBe('grid')
    expect(mode('10:05')).toBe('generator')
    expect(mode('10:10')).toBe('island')
    expect(mode('10:15')).toBe('unknown')
    expect(mode('10:20')).toBe('grid')
    expect(mode('10:30')).toBeNull()
  })

  it('has no mode before the Encombi first reports', () => {
    // KE on 15.11: the controller started at 22:30, on the generator.
    const points = [
      point('grid_breakers_on_bits', at(22, 30), 0),
      point('gen_breakers_on_bits', at(22, 30), 1),
    ]
    const buckets = daySupplyModes(points, anchor, now)!
    expect(buckets.find((b) => b.time === '22:25')?.mode).toBeNull()
    expect(buckets.find((b) => b.time === '00:00')?.mode).toBeNull()
    expect(buckets.find((b) => b.time === '22:30')?.mode).toBe('generator')
  })
})

describe('supplySegments', () => {
  it('merges runs of one mode and skips empty buckets', () => {
    const buckets: DaySupplyBucket[] = [
      { t: 0, time: '10:00', mode: 'grid' },
      { t: 1, time: '10:05', mode: 'grid' },
      { t: 2, time: '10:10', mode: 'generator' },
      { t: 3, time: '10:15', mode: 'generator' },
      { t: 4, time: '10:20', mode: null },
    ]
    expect(supplySegments(buckets)).toEqual([
      { x1: '10:00', x2: '10:10', mode: 'grid' },
      { x1: '10:10', x2: '10:20', mode: 'generator' },
    ])
  })
})
