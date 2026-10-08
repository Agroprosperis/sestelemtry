import { describe, expect, it } from 'vitest'
import type { Constraints } from './dispatchClient'
import {
  actionGroups,
  buildPreview,
  emptyModel,
  hourPlanState,
  makeClock,
  reviewGroups,
  shiftModel,
  splitBessFlow,
  tariffSummary,
  timeLabel,
} from './model'

const cfg: Constraints = {
  reserve_pct: 20,
  grid_charge: true,
  ess_sale: true,
  block_export: false,
  import_cap_kw: 1700,
  export_cap_kw: 850,
}

describe('desk model', () => {
  it('previews the command on the selected hours only', () => {
    const working = emptyModel(cfg)
    const m = buildPreview(working, cfg, 'cover', 300, 'discharge', 2, 4)
    expect(m.commands[1]).toBeNull()
    expect(m.commands[2]).toEqual({ type: 'cover', value: 300, id: 'block-2-4-cover' })
    expect(m.commands[3]?.id).toBe('block-2-4-cover')
    expect(m.commands[4]).toBeNull()
    // «Повернути в AUTO» clears the hours instead of storing an auto command.
    const cleared = buildPreview(m, cfg, 'auto', 0, 'discharge', 2, 3)
    expect(cleared.commands[2]).toBeNull()
    expect(cleared.commands[3]).not.toBeNull()
  })

  it('marks draft, manual and AUTO hours', () => {
    const active = emptyModel(cfg)
    active.commands[0] = { type: 'hold', value: 0, id: 'h' }
    const working = buildPreview(active, cfg, 'fixed', 200, 'charge', 1, 2)
    expect(hourPlanState(active, working, -1).key).toBe('history')
    expect(hourPlanState(active, working, 0).key).toBe('manual')
    expect(hourPlanState(active, working, 1).key).toBe('draft')
    expect(hourPlanState(active, working, 2).key).toBe('auto')
  })

  it('merges equal neighbouring hours into one action band', () => {
    const active = emptyModel(cfg)
    const working = emptyModel(cfg)
    const preview = buildPreview(working, cfg, 'export', 300, 'discharge', 3, 6)
    const groups = actionGroups(preview, active, working, [-1, 0, 1, 2, 3, 4, 5, 6])
    const exportGroup = groups.find((g) => g.cmd?.type === 'export')
    expect(exportGroup).toMatchObject({ start: 3, end: 6, status: 'preview' })
    expect(exportGroup?.mark.name).toBe('Експорт PCC')
    expect(groups[0]).toMatchObject({ start: 0, end: 3, status: 'auto' })
  })

  it('lists load, command and limit changes for the review', () => {
    const active = emptyModel(cfg)
    const working = buildPreview(active, { ...cfg, reserve_pct: 30 }, 'cover', 300, 'discharge', 0, 2)
    working.loads[0] = 200
    working.loads[1] = 200
    const r = reviewGroups(active, working)
    expect(r.loads).toEqual([{ start: 0, end: 2, text: 'Споживання: 200 кВт' }])
    expect(r.commands).toEqual([{ start: 0, end: 2, text: 'Покривати споживання · до 300 кВт' }])
    expect(r.cfgChanged).toBe(true)
  })

  it('shifts a draft when hours go by', () => {
    const m = emptyModel(cfg)
    m.loads[2] = 150
    m.commands[3] = { type: 'hold', value: 0, id: 'h' }
    const later = shiftModel(m, 2)
    expect(later.loads[0]).toBe(150)
    expect(later.commands[1]?.type).toBe('hold')
    expect(later.loads).toHaveLength(24)
  })

  it('splits BESS power by source and destination', () => {
    expect(splitBessFlow(-300, 500, 300)).toEqual([
      { key: 'solar', power: -200 },
      { key: 'grid', power: -100 },
    ])
    expect(splitBessFlow(500, 0, 200)).toEqual([
      { key: 'load', power: 200 },
      { key: 'export', power: 300 },
    ])
  })

  it('labels hours in local time with the next-day prefix', () => {
    // 16:00Z = 19:00 Kyiv (EEST): midnight is 5 hours away.
    const c = makeClock('2026-10-07T16:00:00Z', 'Europe/Kyiv')
    expect(c).toEqual({ nowHour: 19, midnight: 5 })
    expect(timeLabel(c, 0)).toBe('19:00')
    expect(timeLabel(c, 6)).toBe('завтра 01:00')
    expect(timeLabel(c, -3)).toBe('16:00')
  })

  it('spells the tariffs as stored, like the mockup', () => {
    const text = tariffSummary({
      distribution_uah_per_kwh: 2.75218, transmission_uah_per_kwh: 0.74291, supplier_margin_uah_per_kwh: 0,
      supplier_margin_mode: 'abs', supplier_margin_pct: 0, other_fees_uah_per_kwh: 0, export_discount: 0.05,
      degradation_uah_per_kwh: 0.6, include_vat: false, vat_rate: 0.2, roundtrip_efficiency: 0.913,
    })
    expect(text).toBe(
      'Купівля: РДН + 2,75218 розподіл + 0,74291 передача; націнка постачальника та інші платежі — 0 грн/кВт·год. ' +
        'Продаж: РДН − 5%. Знос: 0,6 грн на кВт·год розряду. Без ПДВ. ККД повного циклу: 91,3%.',
    )
  })
})
