import { describe, expect, it } from 'vitest'
import { buildChartSvg, type ChartInput } from './chartSvg'
import { EMPTY_POINT, makeClock, type ChartPoint } from './model'

function input(over: Partial<ChartInput> = {}): ChartInput {
  const clock = makeClock('2026-10-07T16:00:00Z', 'Europe/Kyiv')
  const future = (i: number): ChartPoint =>
    i < 3 ? { p: i === 1 ? 300 : -150, before: 50, soc: 55, import: i === 1 ? 0 : 250, export: i === 1 ? 100 : 0, curtailed: 0 } : EMPTY_POINT
  return {
    width: 900,
    clock,
    first: -8,
    count: 32,
    series: { price: true, pv: true, load: true, grid: true, bess: true, soc: true, before: false, plan: true },
    layout: 'split',
    values: true,
    reserve: 20,
    start: 1,
    end: 2,
    inspectHour: 1,
    editable: true,
    pvAt: (i) => (i < 0 ? 40 : 0),
    loadAt: (i) => (i < 3 ? 200 : null),
    priceAt: (i) => (i < 20 ? 5 + (i % 3) : null),
    previewAt: (i) => (i < 0 ? { p: 120, before: 60, soc: 58, import: 80, export: 0, curtailed: null } : future(i)),
    baselineAt: () => EMPTY_POINT,
    planAt: (i) => (i < 0 ? { ess: 100, soc: 59, grid: 90 } : null),
    actions: [],
    modes: [],
    ...over,
  }
}

describe('desk chart', () => {
  it('splits BESS bars by purpose and keeps PV fact solid, forecast dashed', () => {
    const { svg } = buildChartSvg(input())
    expect(svg).toContain('data-flow="export"')
    expect(svg).toContain('data-flow="load"')
    expect(svg).toContain('data-flow="grid"')
    expect(svg).toMatch(/data-mark="pv-fact"[^>]*stroke-width="2.5"\/>/)
    expect(svg).not.toMatch(/data-mark="pv-fact"[^>]*dasharray/)
    expect(svg).toContain('data-mark="grid"')
    expect(svg).toContain('Резерв 20%')
  })

  it('overlays the plan in force on history hours', () => {
    const { svg } = buildChartSvg(input())
    expect(svg).toContain('data-mark="plan-ess"')
    expect(svg).toContain('data-mark="plan-soc"')
    const off = buildChartSvg(input({ series: { ...input().series, plan: false } }))
    expect(off.svg).not.toContain('data-mark="plan-ess"')
  })

  it('drops editing bands on a read-only day', () => {
    const editable = buildChartSvg(input())
    const readOnly = buildChartSvg(input({ editable: false }))
    expect(editable.svg).toContain('>Дія<')
    expect(readOnly.svg).not.toContain('>Дія<')
    expect(readOnly.svg).not.toContain('d-select-bg')
  })

  it('labels SOC now and at the end of the selection', () => {
    const { svg } = buildChartSvg(
      input({
        start: 4,
        end: 6,
        previewAt: (i) => (i === 0 ? { ...EMPTY_POINT, before: 90 } : i < 0 ? { p: 0, before: 60, soc: 60, import: 0, export: 0, curtailed: null } : EMPTY_POINT),
      }),
    )
    // No forecast in the selection: only the «now» point is labelled.
    expect(svg.match(/class="d-soc-label"/g)?.length).toBe(1)
    expect(svg).toMatch(/class="d-soc-label"[^>]*>90%</)
  })

  it('draws the PV forecast in the dashboard forecast colour', () => {
    const { svg } = buildChartSvg(input({ pvAt: (i) => (i < 0 ? 40 : 120) }))
    expect(svg).toMatch(/data-mark="pv" [^>]*stroke="var\(--d-pv-forecast\)"/)
  })

  it('reports the geometry used for pointer hits', () => {
    const l = buildChartSvg(input())
    expect(l.first).toBe(-8)
    expect(l.count).toBe(32)
    expect(l.left).toBe(46)
    expect(l.step).toBeCloseTo((900 - 43 - 46) / 32)
  })
})
