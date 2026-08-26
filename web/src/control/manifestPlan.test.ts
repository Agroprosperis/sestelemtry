import { describe, expect, it } from 'vitest'
import type { ManifestPayload } from './controlClient'
import { appliedAnnotation, manifestPlanToUzePlan } from './manifestPlan'

function payloadWith(intervals: ManifestPayload['plan'] extends infer P
  ? P extends { intervals: infer I }
    ? I
    : never
  : never): ManifestPayload {
  return {
    schema_version: 'lite-1',
    manifest_id: 'ze-20260826-abcd1234',
    site_id: 'ze',
    issued_at: '2026-08-26T10:00:00Z',
    valid_from: '2026-08-26T10:00:00Z',
    valid_until: '2026-08-28T00:00:00Z',
    mode: 'shadow',
    write_enabled: false,
    preset: 'economic_arbitrage',
    plan: { granularity: '1h', load_source: 'operator', intervals },
  }
}

// The anchor day is expressed in local time — build interval timestamps
// from local Date parts so the test passes in any timezone.
function localIso(anchor: Date, hour: number): string {
  const d = new Date(anchor)
  d.setHours(hour, 0, 0, 0)
  return d.toISOString()
}

describe('manifestPlanToUzePlan', () => {
  const anchor = new Date(2026, 7, 26, 12, 0, 0)

  it('projects intervals of the anchor day and drops other days', () => {
    const tomorrow = new Date(anchor)
    tomorrow.setDate(tomorrow.getDate() + 1)
    const plan = manifestPlanToUzePlan(
      payloadWith([
        { ts: localIso(anchor, 14), ess_kw: 180, soc_target_pct: 65, rdn_uah_per_kwh: 6.5 },
        { ts: localIso(anchor, 15), ess_kw: -120 },
        { ts: localIso(tomorrow, 10), ess_kw: 90 },
      ]),
      anchor,
    )
    expect(plan).not.toBeNull()
    expect(plan!.available).toBe(true)
    expect(plan!.hours).toHaveLength(2)
    expect(plan!.hours[0]).toMatchObject({ hour: 14, recommended_ess_kw: 180, action: 'discharge' })
    expect(plan!.hours[0].soc_pct).toBe(65)
    expect(plan!.hours[1]).toMatchObject({ hour: 15, recommended_ess_kw: -120, action: 'charge' })
    // No soc target → NaN, which the chart projection turns into null.
    expect(Number.isNaN(plan!.hours[1].soc_pct)).toBe(true)
  })

  it('returns null without a plan or without same-day hours', () => {
    expect(manifestPlanToUzePlan(undefined, anchor)).toBeNull()
    const p = payloadWith([])
    expect(manifestPlanToUzePlan(p, anchor)).toBeNull()
    const otherDay = new Date(anchor)
    otherDay.setDate(otherDay.getDate() + 3)
    expect(
      manifestPlanToUzePlan(payloadWith([{ ts: localIso(otherDay, 9), ess_kw: 50 }]), anchor),
    ).toBeNull()
  })
})

describe('appliedAnnotation', () => {
  const anchor = new Date(2026, 7, 26, 12, 0, 0)

  it('rounds applied_at down to the 5-minute bucket label', () => {
    const applied = new Date(anchor)
    applied.setHours(9, 43, 20, 0)
    const a = appliedAnnotation(applied.toISOString(), anchor)
    expect(a).toEqual({ time: '09:40', label: 'manifest applied' })
  })

  it('ignores other days and missing values', () => {
    expect(appliedAnnotation(undefined, anchor)).toBeUndefined()
    const yesterday = new Date(anchor)
    yesterday.setDate(yesterday.getDate() - 1)
    expect(appliedAnnotation(yesterday.toISOString(), anchor)).toBeUndefined()
  })
})
