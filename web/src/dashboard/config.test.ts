import { describe, expect, it } from 'vitest'
import { energyFloorFor, MIN_RELIABLE_DATA_AT, ORGANIZATION_OPERATION_START } from './config'

describe('energyFloorFor', () => {
  it('uses the site commissioning day so imported archive is reachable', () => {
    const floor = energyFloorFor('ab')
    expect(floor.getFullYear()).toBe(2025)
    expect(floor.getMonth()).toBe(5)
    expect(floor.getDate()).toBe(20)
  })

  // Local midnight, not UTC: `new Date('2025-06-20')` is UTC midnight,
  // which east of Greenwich is still 19 June locally and would let one
  // pre-commissioning evening back into the first bucket.
  it('anchors every site at local midnight', () => {
    for (const [id, day] of Object.entries(ORGANIZATION_OPERATION_START)) {
      expect(day.getHours(), id).toBe(0)
      expect(day.getMinutes(), id).toBe(0)
      expect(day.getSeconds(), id).toBe(0)
    }
  })

  it('falls back for a site with no known start day', () => {
    expect(energyFloorFor('demo-org').getTime()).toBe(MIN_RELIABLE_DATA_AT.getTime())
    expect(energyFloorFor('').getTime()).toBe(MIN_RELIABLE_DATA_AT.getTime())
  })

  // A floor later than the site's own start would silently re-hide the
  // archive this map exists to expose.
  it('never lands after the conservative fallback', () => {
    for (const [id, day] of Object.entries(ORGANIZATION_OPERATION_START)) {
      expect(day.getTime(), id).toBeLessThanOrEqual(MIN_RELIABLE_DATA_AT.getTime())
    }
  })
})
