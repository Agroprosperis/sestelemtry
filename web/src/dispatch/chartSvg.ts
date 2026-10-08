// buildChartSvg is the desk chart: a port of drawChart from the approved
// mockup (geometry, bands, label placement), with the series painted in
// the monitoring dashboard's Energy Trend language (ems_manual_control
// _mvp.md §2.2): PV green line (fact solid, forecast dashed), load
// orange, PCC one grey signed zone, BESS bars split by purpose, SOC
// purple on the right axis, RDN a calm price band.

import {
  type ActionGroup,
  type ChartPoint,
  type Clock,
  type PlanOverlay,
  clockAt,
  flowMeta,
  fmt,
  hasValue,
  splitBessFlow,
  timeLabel,
} from './model'

export type SeriesToggles = {
  price: boolean
  pv: boolean
  load: boolean
  grid: boolean
  bess: boolean
  soc: boolean
  before: boolean
  plan: boolean
}

export type ModeGroup = { key: string; label: string; short: string; start: number; end: number }

export type ChartInput = {
  width: number
  clock: Clock
  first: number
  count: number
  series: SeriesToggles
  layout: 'split' | 'combined'
  values: boolean
  reserve: number
  start: number
  end: number
  inspectHour: number
  editable: boolean
  pvAt: (i: number) => number | null
  loadAt: (i: number) => number | null
  priceAt: (i: number) => number | null
  previewAt: (i: number) => ChartPoint
  baselineAt: (i: number) => ChartPoint
  planAt?: (i: number) => PlanOverlay | null
  actions: ActionGroup[]
  modes: ModeGroup[]
}

export type ChartLayout = {
  svg: string
  left: number
  step: number
  first: number
  count: number
  overflow: ActionGroup[]
}

const esc = (s: string | number): string =>
  String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

const modeColors: Record<string, { fill: string; ink: string }> = {
  manual: { fill: 'var(--d-mode-manual)', ink: 'var(--d-mode-manual-ink)' },
  draft: { fill: 'var(--d-mode-draft)', ink: 'var(--d-mode-draft-ink)' },
  auto: { fill: 'var(--d-mode-auto)', ink: 'var(--d-mode-auto-ink)' },
  history: { fill: 'var(--d-history-bg)', ink: 'var(--d-muted)' },
}

export function buildChartSvg(c: ChartInput): ChartLayout {
  const s = c.series
  const w = Math.max(240, c.width)
  const small = w < 490
  const L = small ? 36 : 46
  const R = s.soc ? 43 : small ? 12 : 36
  const right = w - R
  const pw = right - L
  const { first, count } = c
  const step = pw / count
  const midnight = c.clock.midnight
  const visible = Array.from({ length: count }, (_, k) => first + k)
  const combined = c.layout === 'combined'
  const contextOn = s.price || s.pv || s.load || s.grid
  const batteryOn = s.bess || s.soc
  const split = !combined && contextOn && batteryOn
  const timeLanes = Math.max(1, Math.ceil(23 / step))
  const top = 24
  const bottom = 395
  const actionY = bottom + timeLanes * 16 + 11
  const modeY = actionY + 39
  const total = c.editable ? modeY + 23 : bottom + timeLanes * 16 + 12
  const priceLanes = Math.max(1, Math.ceil(32 / step))
  const labelRoom = s.price && c.values ? priceLanes * 14 + 12 : 12
  const priceStrip = combined && s.price && (s.pv || s.load || s.grid || batteryOn)
  const stripH = priceStrip ? labelRoom + 13 : 0
  const h1 = split ? 151 : bottom - top
  const y2 = split ? top + h1 + timeLanes * 16 + 24 : top + stripH + (priceStrip ? 27 : 0)
  const h2 = bottom - y2
  const reserve = c.reserve
  const anyPower = s.bess || (combined && (s.pv || s.load || s.grid))
  const x = (i: number) => L + (i - first + 0.5) * step
  const bx = (i: number) => L + (i - first) * step
  const gridNetAt = (i: number): number | null => {
    const v = c.previewAt(i)
    return hasValue(v.import) && hasValue(v.export) ? v.import - v.export : null
  }
  const nums = (xs: (number | null)[]) => xs.filter(hasValue)
  const powerMax =
    Math.ceil(
      Math.max(
        400,
        ...nums(
          visible.flatMap((i) => [
            ...(s.pv ? [c.pvAt(i)] : []),
            ...(s.load ? [c.loadAt(i)] : []),
            ...(s.grid ? [c.previewAt(i).import] : []),
          ]),
        ),
      ) / 100,
    ) * 100
  const powerMin =
    s.grid && !combined
      ? Math.floor(Math.min(0, ...visible.map((i) => gridNetAt(i) ?? 0)) / 100) * 100
      : 0
  const powers = nums(
    visible.flatMap((i) => [
      ...(s.bess ? [c.previewAt(i).p, ...(s.before ? [c.baselineAt(i).p] : [])] : []),
      ...(s.bess && s.plan && c.planAt ? [c.planAt(i)?.ess ?? null] : []),
      ...(combined && s.pv ? [c.pvAt(i)] : []),
      ...(combined && s.load ? [c.loadAt(i)] : []),
      ...(combined && s.grid ? [gridNetAt(i)] : []),
    ]),
  )
  const powerHigh = Math.ceil(Math.max(300, ...powers) / 100) * 100
  const powerLow = Math.floor(Math.min(-100, ...powers) / 100) * 100
  const by = (v: number) => y2 + h2 - 8 - ((v - powerLow) / (powerHigh - powerLow)) * (h2 - 16)
  const sy = (v: number) => y2 + h2 - 8 - (v / 100) * (h2 - 16)
  const py = combined ? by : (v: number) => top + h1 - 5 - ((v - powerMin) / (powerMax - powerMin)) * (h1 - labelRoom - 5)
  const priceMax = Math.max(15, ...nums(visible.map((i) => c.priceAt(i))))
  const priceY = (v: number) =>
    priceStrip ? top + stripH - 3 - (v / priceMax) * 14 : top + h1 - 5 - (v / priceMax) * (h1 - labelRoom - 5)

  const path = (at: (i: number) => number | null, y: (v: number) => number, keep: (i: number) => boolean = () => true) => {
    let connected = false
    return visible
      .map((i) => {
        const value = keep(i) ? at(i) : null
        if (!hasValue(value)) {
          connected = false
          return ''
        }
        const command = (connected ? 'L' : 'M') + x(i).toFixed(1) + ',' + y(value).toFixed(1)
        connected = true
        return command
      })
      .join(' ')
      .trim()
  }
  const socPath = (at: (i: number) => ChartPoint) => {
    let connected = false
    return visible
      .map((i) => {
        const value = at(i)
        if (!hasValue(value.soc)) {
          connected = false
          return ''
        }
        const beginning = connected
          ? ''
          : hasValue(value.before)
            ? 'M' + bx(i) + ',' + sy(value.before) + ' '
            : 'M' + bx(i + 1) + ',' + sy(value.soc) + ' '
        connected = true
        return beginning + 'L' + bx(i + 1) + ',' + sy(value.soc)
      })
      .join(' ')
      .trim()
  }

  let svg =
    '<svg viewBox="0 0 ' + w + ' ' + total + '" xmlns="http://www.w3.org/2000/svg">' +
    '<title>Керування УЗЕ: потужність у кВт без знаків. Розряд над нулем, заряд під нулем. SOC за правою шкалою 0–100%.</title>'
  const line = (x1: number, y1: number, x2: number, y2b: number, extra = '') =>
    '<line x1="' + x1 + '" y1="' + y1 + '" x2="' + x2 + '" y2="' + y2b + '" ' + extra + '/>'
  const text = (v: string | number, xx: number, yy: number, extra = '') =>
    '<text x="' + xx + '" y="' + yy + '" ' + extra + '>' + esc(v) + '</text>'
  const selA = Math.max(first, c.start)
  const selB = Math.min(first + count, c.end)
  const panels: [number, number][] = split
    ? [
        [top, h1],
        [y2, h2],
      ]
    : priceStrip
      ? [
          [top, stripH],
          [y2, h2],
        ]
      : [[top, bottom - top]]
  const historyEnd = Math.min(0, first + count)
  panels.forEach(([yy, hh]) => {
    svg += '<rect x="' + L + '" y="' + yy + '" width="' + pw + '" height="' + hh + '" fill="var(--d-chart-bg)" stroke="var(--d-line)"/>'
    if (historyEnd > first)
      svg += '<rect x="' + L + '" y="' + yy + '" width="' + (historyEnd - first) * step + '" height="' + hh + '" fill="var(--d-history-bg)"/>'
    const todayA = Math.max(0, first)
    const todayB = Math.min(midnight, first + count)
    if (todayB > todayA)
      svg += '<rect x="' + bx(todayA) + '" y="' + yy + '" width="' + (todayB - todayA) * step + '" height="' + hh + '" fill="var(--d-today-bg)"/>'
    if (c.editable && selB > selA)
      svg += '<rect x="' + bx(selA) + '" y="' + yy + '" width="' + (selB - selA) * step + '" height="' + hh + '" fill="var(--d-select-bg)"/>'
    for (let i = first + 1; i < first + count; i++) svg += line(bx(i), yy, bx(i), yy + hh, 'stroke="var(--d-hour-line)"')
    if (c.editable && selB > selA)
      [selA, selB].forEach((i) => (svg += line(bx(i), yy, bx(i), yy + hh, 'stroke="var(--d-select-edge)" stroke-width="2"')))
  })
  if (!combined && contextOn && (s.pv || s.load || s.grid)) {
    svg += text('кВт', L, top - 8, 'class="d-axis-title"')
    const ticks = [0, powerMax / 2, powerMax]
    if (powerMin < 0 && py(powerMin) - py(0) >= 14) ticks.unshift(powerMin)
    ticks.forEach((v) => {
      const zero = v === 0 && powerMin < 0
      svg += line(L, py(v), right, py(v), 'stroke="' + (zero ? 'var(--d-zero)' : 'var(--d-line)') + '"' + (zero ? ' stroke-width="1.2"' : ''))
      svg += text(v, L - 8, py(v) + 4, 'text-anchor="end"')
    })
  }
  if (s.price && !priceStrip && !small)
    [0, priceMax / 2, priceMax].forEach((v) => (svg += text(fmt(v), right + 5, priceY(v) + 4)))
  if (s.price) {
    svg += text('РДН · грн/кВт·год', right, top - 8, 'class="d-axis-title" text-anchor="end"')
    const priceBottom = priceStrip ? top + stripH - 3 : top + h1 - 5
    visible.forEach((i) => {
      const p = c.priceAt(i)
      if (!hasValue(p)) return
      svg +=
        '<rect data-mark="price" x="' + (bx(i) + step * 0.17) + '" y="' + priceY(p) + '" width="' + step * 0.66 +
        '" height="' + (priceBottom - priceY(p)) + '" fill="var(--d-price)" opacity="var(--d-price-opacity)"/>'
    })
    visible.forEach((i, k) => {
      const p = c.priceAt(i)
      if (c.values && hasValue(p))
        svg += text(fmt(p), x(i), top + 13 + (k % priceLanes) * 14, 'text-anchor="middle" class="d-price-label' + (p >= 12 ? ' is-high' : '') + '"')
    })
  }
  if (batteryOn || combined) {
    if (anyPower) {
      svg += text(combined ? 'Потужність · кВт' : 'УЗЕ · кВт', L, y2 - 10, 'class="d-axis-title"')
      ;[powerLow, 0, powerHigh].forEach((v) => {
        svg += line(L, by(v), right, by(v), 'stroke="' + (v === 0 ? 'var(--d-zero)' : 'var(--d-line)') + '" stroke-width="' + (v === 0 ? 1.2 : 1) + '"')
        svg += text(Math.abs(v), L - 8, by(v) + 4, 'text-anchor="end"')
      })
    }
    if (s.soc) {
      svg += text('SOC · %', right, y2 - 10, 'class="d-axis-title d-soc-axis" text-anchor="end"')
      ;[0, 25, 50, 75, 100].forEach((v) => {
        if (!anyPower) svg += line(L, sy(v), right, sy(v), 'stroke="var(--d-line)"')
        svg += line(right, sy(v), right + 4, sy(v), 'stroke="var(--d-soc)"')
        svg += text(v + '%', right + 7, sy(v) + 4, 'class="d-soc-axis"')
      })
      const reserveStart = first < 0 ? bx(0) : L
      svg += '<rect x="' + reserveStart + '" y="' + sy(reserve) + '" width="' + (right - reserveStart) + '" height="' + (sy(0) - sy(reserve)) + '" fill="var(--d-red)" opacity=".05"/>'
      svg += line(reserveStart, sy(reserve), right, sy(reserve), 'stroke="var(--d-red)" stroke-width="1.5"')
      const reserveLabelY = sy(reserve) + 16
      svg +=
        '<rect x="' + (right - 107) + '" y="' + (reserveLabelY - 13) + '" width="103" height="17" rx="2" fill="var(--d-reserve-bg)"/>' +
        text('Резерв ' + fmt(reserve) + '%', right - 6, reserveLabelY, 'class="d-reserve-label" text-anchor="end"')
    }
    if (s.bess) {
      visible.forEach((i) => {
        const v = c.previewAt(i)
        const load = c.loadAt(i)
        const pv = c.pvAt(i)
        if (!hasValue(v.p) || Math.abs(v.p) < 0.05) return
        let cumulative = 0
        const parts = hasValue(load) && hasValue(pv) ? splitBessFlow(v.p, pv, load) : [{ key: v.p < 0 ? 'grid' : 'load', power: v.p } as const]
        parts.forEach((part) => {
          const from = cumulative
          cumulative += part.power
          svg +=
            '<rect data-mark="bess" data-flow="' + part.key + '" x="' + (bx(i) + step * 0.22) + '" y="' + Math.min(by(from), by(cumulative)) +
            '" width="' + step * 0.56 + '" height="' + Math.abs(by(cumulative) - by(from)) + '" fill="' + flowMeta[part.key].color + '"' +
            (i < 0 ? ' opacity=".8"' : '') + '><title>' + esc(timeLabel(c.clock, i) + ' · ' + flowMeta[part.key].name + ': ' + fmt(Math.abs(part.power)) + ' кВт') + '</title></rect>'
        })
      })
      if (s.before) {
        let connected = false
        const beforeSteps = visible
          .map((i) => {
            const p = c.baselineAt(i).p
            if (i < 0 || !hasValue(p)) {
              connected = false
              return ''
            }
            const segment = (connected ? 'L' : 'M') + bx(i) + ',' + by(p) + ' L' + bx(i + 1) + ',' + by(p)
            connected = true
            return segment
          })
          .join(' ')
          .trim()
        if (beforeSteps) svg += '<path data-mark="before" d="' + beforeSteps + '" stroke="var(--d-old)" stroke-width="2" fill="none"/>'
      }
      if (s.plan && c.planAt) {
        let connected = false
        const planSteps = visible
          .map((i) => {
            const p = c.planAt?.(i)?.ess
            if (!hasValue(p)) {
              connected = false
              return ''
            }
            const segment = (connected ? 'L' : 'M') + bx(i) + ',' + by(p) + ' L' + bx(i + 1) + ',' + by(p)
            connected = true
            return segment
          })
          .join(' ')
          .trim()
        if (planSteps) svg += '<path data-mark="plan-ess" d="' + planSteps + '" stroke="var(--d-plan)" stroke-width="2" stroke-dasharray="4 3" fill="none"/>'
      }
    }
  }
  if (s.grid) {
    const base = py(0)
    const runs: { s: number; vals: number[] }[] = []
    let run: { s: number; vals: number[] } | null = null
    visible.forEach((i) => {
      const n = gridNetAt(i)
      if (n === null) {
        run = null
        return
      }
      if (!run) {
        run = { s: i, vals: [] }
        runs.push(run)
      }
      run.vals.push(n)
    })
    runs.forEach((r) => {
      let edge = ''
      r.vals.forEach((v, k) => {
        edge += (k ? ' L' : 'M') + bx(r.s + k) + ',' + py(v).toFixed(1) + ' L' + bx(r.s + k + 1) + ',' + py(v).toFixed(1)
      })
      svg += '<path d="' + edge + ' L' + bx(r.s + r.vals.length) + ',' + base.toFixed(1) + ' L' + bx(r.s) + ',' + base.toFixed(1) + ' Z" fill="var(--d-grid)" opacity=".2"/>'
      svg += '<path data-mark="grid" d="' + edge + '" fill="none" stroke="var(--d-grid-ink)" stroke-width="2" stroke-linejoin="round"/>'
    })
    if (s.plan && c.planAt) {
      const planGrid = path((i) => c.planAt?.(i)?.grid ?? null, py)
      if (planGrid) svg += '<path data-mark="plan-grid" d="' + planGrid + '" fill="none" stroke="var(--d-grid-ink)" stroke-width="1.5" stroke-dasharray="4 3"/>'
    }
    if (!combined && powerMin < 0 && !small) {
      svg += text('імпорт ↑', L + 6, py(0) - 7, 'class="d-grid-hint"')
      svg += text('експорт ↓', L + 6, py(0) + 14, 'class="d-grid-hint"')
    }
  }
  if (s.pv) {
    const fact = path(c.pvAt, py, (i) => i < 0)
    const forecast = path(c.pvAt, py, (i) => i >= 0)
    if (forecast)
      svg += '<path d="' + forecast + ' L' + x(first + count - 1) + ',' + py(0) + ' L' + x(Math.max(0, first)) + ',' + py(0) + ' Z" fill="var(--d-pv)" opacity=".06"/>'
    if (fact) svg += '<path data-mark="pv-fact" d="' + fact + '" stroke="var(--d-pv)" fill="none" stroke-width="2.5"/>'
    if (forecast) svg += '<path data-mark="pv" d="' + forecast + '" stroke="var(--d-pv)" fill="none" stroke-width="2.5" stroke-dasharray="5 4"/>'
  }
  if (s.load) {
    const loadPath = path(c.loadAt, py)
    if (loadPath) svg += '<path data-mark="load" d="' + loadPath + '" stroke="var(--d-load)" fill="none" stroke-width="2.5"/>'
    visible.forEach((i, k) => {
      const value = c.loadAt(i)
      if (hasValue(value) && (k === 0 || !hasValue(c.loadAt(i - 1))) && (k === count - 1 || !hasValue(c.loadAt(i + 1))))
        svg += '<circle data-mark="load-point" cx="' + x(i) + '" cy="' + py(value) + '" r="3" fill="var(--d-load)"/>'
    })
  }
  if (s.bess && c.values) {
    const placed: { x: number; y: number; w: number; h: number }[] = []
    visible.forEach((i) => {
      const p = c.previewAt(i).p
      if (!hasValue(p) || Math.abs(p) < 0.05) return
      const label = fmt(Math.abs(p))
      const labelWidth = label.length * 7 + 4
      const xx = Math.max(L + labelWidth / 2 + 2, Math.min(right - labelWidth / 2 - 2, x(i)))
      const tip = by(p)
      const preferred = tip + (p > 0 ? -7 : 16)
      let chosen: { yy: number; box: { x: number; y: number; w: number; h: number } } | null = null
      for (const offset of [0, -16, 16, -32, 32, -48, 48, -64, 64]) {
        const yy = Math.max(y2 + 16, Math.min(bottom - 6, preferred + offset))
        const box = { x: xx - labelWidth / 2, y: yy - 12, w: labelWidth, h: 15 }
        if (!placed.some((b) => box.x < b.x + b.w + 3 && box.x + box.w + 3 > b.x && box.y < b.y + b.h + 2 && box.y + box.h + 2 > b.y)) {
          chosen = { yy, box }
          break
        }
      }
      if (!chosen) return
      placed.push(chosen.box)
      if (Math.abs(chosen.yy - preferred) > 17) svg += line(x(i), tip, xx, chosen.yy - 5, 'stroke="var(--d-muted)" stroke-width="1" opacity=".65"')
      svg += text(label, xx, chosen.yy, 'text-anchor="middle" class="d-power-label' + (p < 0 ? ' is-charge' : '') + '"')
    })
  }
  if (s.soc) {
    const beforeSoc = socPath((i) => (i < 0 ? { ...c.baselineAt(i), soc: null } : c.baselineAt(i)))
    const previewSoc = socPath(c.previewAt)
    if (s.before && beforeSoc) svg += '<path data-mark="before" d="' + beforeSoc + '" fill="none" stroke="var(--d-old)" stroke-width="2"/>'
    if (s.plan && c.planAt) {
      const planSoc = path((i) => c.planAt?.(i)?.soc ?? null, sy)
      if (planSoc) svg += '<path data-mark="plan-soc" d="' + planSoc + '" fill="none" stroke="var(--d-soc)" stroke-width="2" stroke-dasharray="4 3" opacity=".8"/>'
    }
    if (previewSoc)
      svg += '<path d="' + previewSoc + '" fill="none" stroke="var(--d-chart-bg)" stroke-width="5.5"/><path data-mark="soc" d="' + previewSoc + '" fill="none" stroke="var(--d-soc)" stroke-width="3.5"/>'
    if (c.editable) {
      const startPoint = c.previewAt(Math.max(first, c.start))
      const endPoint = c.previewAt(Math.min(first + count, c.end) - 1)
      const checkpoints = [
        { i: Math.max(first, c.start), v: startPoint.before },
        { i: Math.min(first + count, c.end), v: endPoint.soc },
      ].filter((p): p is { i: number; v: number } => hasValue(p.v))
      checkpoints.forEach((p, k) => {
        if (p.i < first || p.i > first + count || (k && Math.abs(bx(p.i) - bx(checkpoints[0].i)) < 68)) return
        const xx = bx(p.i)
        const yy = sy(p.v)
        svg += '<circle cx="' + xx + '" cy="' + yy + '" r="4" fill="var(--d-soc)" stroke="var(--d-chart-bg)" stroke-width="2"/>'
        svg += text(fmt(p.v) + '%', Math.min(right - 3, Math.max(L + 3, xx)), yy + (p.v > 88 ? 19 : -10),
          'class="d-soc-label" text-anchor="' + (xx < L + 25 ? 'start' : xx > right - 25 ? 'end' : 'middle') + '"')
      })
    }
  }
  if (!contextOn && !batteryOn) svg += text('Увімкніть показник над графіком', L + pw / 2, top + (bottom - top) / 2, 'text-anchor="middle"')
  const timeAxis = (yy: number) => {
    svg += '<rect x="' + L + '" y="' + yy + '" width="' + pw + '" height="' + (timeLanes * 16 + 8) + '" fill="var(--d-axis-bg)"/>'
    visible.forEach((i, k) => {
      svg += line(bx(i), yy, bx(i), yy + 4, 'stroke="var(--d-muted)"')
      const style = i === 0 && c.editable ? ' style="fill:var(--d-now)"' : i === midnight ? ' style="fill:var(--d-soc)"' : ''
      svg += text(clockAt(c.clock, i).slice(0, 2), x(i), yy + 16 + (k % timeLanes) * 16, 'text-anchor="middle" class="d-hour-label"' + style)
    })
  }
  timeAxis(bottom)
  if (split) timeAxis(top + h1)

  const overflow: ActionGroup[] = []
  if (c.editable) {
    svg += text('Дія', L - 5, actionY + 21, 'text-anchor="end" class="d-band-label"')
    c.actions.forEach((g) => {
      const xx = bx(g.start) + 1
      const ww = (g.end - g.start) * step - 2
      const mark = g.mark
      const full = Math.max(mark.name.length, mark.value.length) * 6.6 + 10 <= ww
      const label = full ? mark.name : mark.short
      const value = full ? mark.value : mark.compact
      const fits = Math.max(label.length, value.length) * 6.6 + 8 <= ww
      const preview = g.status === 'preview'
      svg +=
        '<rect data-command-status="' + g.status + '" x="' + xx + '" y="' + actionY + '" width="' + Math.max(0, ww) +
        '" height="34" rx="3" fill="' + (preview ? 'var(--d-select-bg)' : 'var(--d-band-bg)') + '"><title>' +
        esc(timeLabel(c.clock, g.start) + '–' + timeLabel(c.clock, g.end) + ': ' + mark.detail) + '</title></rect>'
      const ink = preview ? 'var(--d-select-ink)' : 'var(--d-band-ink)'
      if (fits) {
        svg += text(label, xx + ww / 2, actionY + (value ? 13 : 22), 'text-anchor="middle" style="fill:' + ink + ';font-size:11px;font-weight:500"')
        if (value) svg += text(value, xx + ww / 2, actionY + 27, 'text-anchor="middle" style="fill:' + ink + ';font-size:11px"')
      } else if (g.cmd || g.status !== 'auto') {
        overflow.push(g)
        if (ww >= 14) svg += text(String(overflow.length), xx + ww / 2, actionY + 22, 'text-anchor="middle" style="fill:' + ink + ';font-size:11px"')
      }
    })
    c.modes.forEach((g) => {
      const xx = bx(g.start) + 1
      const ww = (g.end - g.start) * step - 2
      const col = modeColors[g.key] ?? modeColors.auto
      const label = ww >= g.label.length * 7 + 12 ? g.label : ww >= 13 ? g.short : ''
      svg += '<rect data-plan-status="' + g.key + '" x="' + xx + '" y="' + modeY + '" width="' + Math.max(0, ww) + '" height="19" rx="3" fill="' + col.fill + '"/>'
      if (label) svg += text(label, xx + ww / 2, modeY + 13, 'text-anchor="middle" style="fill:' + col.ink + ';font-size:11px;font-weight:500"')
    })
  }
  if (c.editable && first <= 0 && first + count > 0) {
    svg += line(bx(0), top, bx(0), bottom, 'stroke="var(--d-now)" stroke-width="1.5"')
    if (!small) svg += text('зараз', bx(0) + 4, combined ? y2 + 17 : top + labelRoom + 13, 'style="fill:var(--d-now)"')
  }
  if (first < midnight && first + count > midnight) {
    svg += line(bx(midnight), top, bx(midnight), bottom, 'stroke="var(--d-soc)" stroke-width="1.5"')
    if (!small) svg += text(c.editable ? 'завтра' : 'північ', bx(midnight) + 4, combined ? y2 + 17 : top + labelRoom + 13, 'style="fill:var(--d-soc)"')
  }
  if (c.inspectHour >= first && c.inspectHour < first + count)
    svg += line(x(c.inspectHour), top, x(c.inspectHour), bottom, 'stroke="var(--d-muted)" opacity=".55"')
  svg += '<rect data-brush="true" x="' + L + '" y="' + top + '" width="' + pw + '" height="' + (total - top) + '" fill="transparent" style="cursor:' + (c.editable ? 'crosshair' : 'default') + '"/></svg>'
  return { svg, left: L, step, first, count, overflow }
}
