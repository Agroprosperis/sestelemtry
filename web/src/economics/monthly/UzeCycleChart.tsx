import { useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { EconomicsUzeCycle, EconomicsUzeCycleChart } from '../../api'

// Колірна палітра серій (порт client_js/uze_cycle_chart.js).
const C = {
  disLoad: '#16a34a',
  disGrid: '#ea580c',
  chgPv: '#facc15',
  chgGrid: '#9ca3af',
  rdn: '#3b82f6',
  socOpt: '#9333ea',
  socFact: '#c084fc',
  fact: '#334155',
  money: '#166534',
  zero: '#475569',
} as const

type SeriesKey = 's-load' | 's-exp' | 's-sun' | 's-grid' | 's-socopt' | 's-socfact' | 's-fact' | 's-rdn'

function fmtUah(n: number): string {
  const s = n < 0 ? '−' : ''
  return s + Math.abs(Math.round(n)).toLocaleString('uk-UA') + ' ₴'
}
const fmtN = (n: number) => Math.round(n).toLocaleString('uk-UA')

function arr(a: number[] | undefined, n: number): number[] {
  return a && a.length ? a : new Array(n).fill(0)
}

type TipRow = { label: string; value: string; swatch?: string; total?: boolean }
type Tip = { head: string; rows: TipRow[] }

// CycleChartSvg renders one cycle's погодинний chart: stacked discharge
// (above the axis) / charge (below), RDN background, optional SOC lines and
// the realised-fact line, with a cursor-following tooltip and a legend that
// toggles series. SOC lines start hidden, matching the original embed.
function CycleChartSvg({ chart }: { chart: EconomicsUzeCycleChart }) {
  const canvasRef = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<number | null>(null)
  const [pos, setPos] = useState<{ x: number; y: number }>({ x: 0, y: 0 })
  const [hidden, setHidden] = useState<Set<SeriesKey>>(() => new Set<SeriesKey>(['s-socopt', 's-socfact']))

  const model = useMemo(() => {
    const labels = chart.labels ?? []
    const n = labels.length
    const o = chart.optimal
    const f = chart.fact
    const optS = chart.summary?.optimal
    const factS = chart.summary?.fact
    const pwr = chart.power_kw || 324
    const cap = chart.capacity_kwh || 516

    const oL = arr(o.to_load_kwh, n)
    const oG = arr(o.to_grid_kwh, n)
    const oCp = arr(o.chg_pv_kwh, n)
    const oCg = arr(o.chg_grid_kwh, n)
    const fEss = arr(f.ess_kw, n)
    const socO = o.soc_pct ?? new Array(n).fill(null)
    const socF = f.soc_pct ?? new Array(n).fill(null)
    const rdn = arr(f.rdn, n)
    const exportUah = arr(o.export_uah, n)
    const loadUah = arr(o.load_uah, n)
    const gridCostUah = arr(o.grid_cost_uah, n)
    const disUah = (i: number) => (exportUah[i] || 0) + (loadUah[i] || 0)

    const has = {
      load: oL.some((v) => v > 0.5),
      exp: oG.some((v) => v > 0.5),
      sun: oCp.some((v) => v > 0.5),
      grid: oCg.some((v) => v > 0.5),
      socopt: socO.some((v) => v != null),
      socfact: socF.some((v) => v != null),
      fact: fEss.some((v) => Math.abs(v) > 0.5),
    }

    let maxUp = 1
    let maxDn = 1
    let rdnMax = 1
    for (let i = 0; i < n; i++) {
      maxUp = Math.max(maxUp, oL[i] + oG[i], Math.max(0, fEss[i]))
      maxDn = Math.max(maxDn, oCp[i] + oCg[i], Math.max(0, -fEss[i]))
      rdnMax = Math.max(rdnMax, rdn[i])
    }

    const W = 940
    const H = 308
    const mt = 30
    const mr = 44
    const mb = 56
    const ml = 40
    const ix0 = ml
    const ix1 = W - mr
    const iy0 = mt
    const iy1 = H - mb
    const iw = ix1 - ix0
    const ih = iy1 - iy0
    const zeroY = iy0 + ih * 0.6
    const k = Math.min((zeroY - iy0) / maxUp, (iy1 - zeroY) / maxDn)
    const slot = iw / Math.max(n, 1)
    const cx = (i: number) => ix0 + (i + 0.5) * slot
    const ySoc = (p: number) => iy1 - (p / 100) * ih
    const bw = Math.min(26, slot * 0.62)

    const tips: Tip[] = []
    for (let i = 0; i < n; i++) {
      const dis = oL[i] + oG[i]
      const chg = oCp[i] + oCg[i]
      const rows: TipRow[] = [{ label: 'Ціна РДН', value: `${rdn[i].toFixed(2)} ₴` }]
      if (dis > 0.5) {
        rows.push({ label: 'На споживання', value: `${fmtN(oL[i])} кВт·год`, swatch: C.disLoad })
        rows.push({ label: 'На експорт', value: `${fmtN(oG[i])} кВт·год`, swatch: C.disGrid })
        rows.push({ label: 'Дохід розряду', value: fmtUah(disUah(i)), total: true })
      }
      if (chg > 0.5) {
        if (oCp[i] > 0.5) rows.push({ label: 'Заряд від сонця', value: `${fmtN(oCp[i])} кВт·год`, swatch: C.chgPv })
        if (oCg[i] > 0.5) rows.push({ label: 'Заряд від мережі', value: `${fmtN(oCg[i])} кВт·год`, swatch: C.chgGrid })
        if (gridCostUah[i] > 0.5) rows.push({ label: 'Вартість заряду з мережі', value: fmtUah(-gridCostUah[i]), total: true })
      }
      if (socO[i] != null) rows.push({ label: 'SOC оптимум', value: `${Math.round(socO[i] as number)}%` })
      if (socF[i] != null) rows.push({ label: 'SOC факт', value: `${Math.round(socF[i] as number)}%` })
      tips.push({ head: labels[i], rows })
    }

    const linePts = (a: number[], yfn: (v: number) => number) =>
      a.map((v, i) => `${cx(i).toFixed(1)},${yfn(v).toFixed(1)}`).join(' ')
    const socLine = (a: Array<number | null>, start: number | null | undefined) => {
      const pts: string[] = []
      if (start != null) pts.push(`${ix0.toFixed(1)},${ySoc(start).toFixed(1)}`)
      a.forEach((v, i) => {
        if (v != null) pts.push(`${(ix0 + (i + 1) * slot).toFixed(1)},${ySoc(v).toFixed(1)}`)
      })
      return pts.join(' ')
    }

    const reserve = (optS?.effect || 0) - (factS?.effect || 0)
    const captured = optS?.effect ? Math.round(((factS?.effect || 0) / optS.effect) * 1000) / 10 : 0

    return {
      n, oL, oG, oCp, oCg, fEss, socO, socF, rdn, disUah,
      has, maxUp, maxDn, rdnMax,
      W, H, ix0, ix1, iy0, iy1, ih, zeroY, k, slot, cx, ySoc, bw,
      tips, linePts, socLine, optS, factS, reserve, captured, pwr, cap,
      socOptStart: o.soc_start, socFactStart: f.soc_start,
    }
  }, [chart])

  const m = model
  if (!m.n) return null
  const vis = (key: SeriesKey) => !hidden.has(key)

  const toggle = (key: SeriesKey) =>
    setHidden((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })

  const onMove = (e: React.MouseEvent) => {
    const canvas = canvasRef.current
    if (!canvas) return
    const r = canvas.getBoundingClientRect()
    let x = e.clientX - r.left + 14
    const y = e.clientY - r.top + 12
    if (x + 190 > r.width) x = e.clientX - r.left - 200
    setPos({ x, y })
  }

  // RDN background columns + per-hour price label.
  const rdnCols: ReactNode[] = []
  for (let i = 0; i < m.n; i++) {
    const x = m.ix0 + i * m.slot + 1
    const h = (m.rdn[i] / m.rdnMax) * m.ih
    const y = m.iy1 - h
    rdnCols.push(<rect key={`r${i}`} x={x.toFixed(1)} y={y.toFixed(1)} width={(m.slot - 2).toFixed(1)} height={h.toFixed(1)} fill={C.rdn} fillOpacity={0.13} />)
    rdnCols.push(
      <text key={`rt${i}`} x={m.cx(i).toFixed(1)} y={(m.iy1 + 16).toFixed(1)} textAnchor="middle" fontSize={9} fill="#1d4ed8" opacity={0.8}>
        {m.rdn[i].toFixed(1)}
      </text>,
    )
  }

  const gLoad: ReactNode[] = []
  const gExp: ReactNode[] = []
  const gSun: ReactNode[] = []
  const gGrid: ReactNode[] = []
  const money: ReactNode[] = []
  const xlab: ReactNode[] = []
  const hoverRects: ReactNode[] = []
  for (let i = 0; i < m.n; i++) {
    const c = m.cx(i)
    const x = c - m.bw / 2
    let acc = 0
    if (m.oL[i] > 0.5) {
      const hh = m.oL[i] * m.k
      gLoad.push(<rect key={i} x={x.toFixed(1)} y={(m.zeroY - acc - hh).toFixed(1)} width={m.bw.toFixed(1)} height={hh.toFixed(1)} fill={C.disLoad} rx={1} />)
      acc += hh
    }
    if (m.oG[i] > 0.5) {
      const hh = m.oG[i] * m.k
      gExp.push(<rect key={i} x={x.toFixed(1)} y={(m.zeroY - acc - hh).toFixed(1)} width={m.bw.toFixed(1)} height={hh.toFixed(1)} fill={C.disGrid} rx={1} />)
      acc += hh
    }
    let accd = 0
    if (m.oCp[i] > 0.5) {
      const hh = m.oCp[i] * m.k
      gSun.push(<rect key={i} x={x.toFixed(1)} y={(m.zeroY + accd).toFixed(1)} width={m.bw.toFixed(1)} height={hh.toFixed(1)} fill={C.chgPv} rx={1} />)
      accd += hh
    }
    if (m.oCg[i] > 0.5) {
      const hh = m.oCg[i] * m.k
      gGrid.push(<rect key={i} x={x.toFixed(1)} y={(m.zeroY + accd).toFixed(1)} width={m.bw.toFixed(1)} height={hh.toFixed(1)} fill={C.chgGrid} rx={1} />)
    }
    const dis = m.oL[i] + m.oG[i]
    if (dis > 3 && m.disUah(i) >= 5) {
      money.push(
        <text key={i} x={c.toFixed(1)} y={(m.zeroY - acc - 4).toFixed(1)} textAnchor="middle" fontSize={10.5} fontWeight={700} fill={C.money}>
          {fmtN(m.disUah(i))}
        </text>,
      )
    }
    const label = m.tips[i]?.head ?? ''
    xlab.push(
      <text key={i} x={c.toFixed(1)} y={(m.iy1 + 36).toFixed(1)} textAnchor="middle" fontSize={11} fill="#334155" fontWeight={600}>
        {label.split(' ')[1] ?? label}
      </text>,
    )
    hoverRects.push(
      <rect
        key={i}
        x={(m.ix0 + i * m.slot).toFixed(1)}
        y={m.iy0}
        width={m.slot.toFixed(1)}
        height={m.ih.toFixed(1)}
        fill="transparent"
        onMouseEnter={() => setHover(i)}
        onMouseMove={onMove}
        onMouseLeave={() => setHover(null)}
      />,
    )
  }

  // Y-axis gridlines + labels.
  const ax: ReactNode[] = []
  ;[m.maxUp, m.maxUp / 2].forEach((v, idx) => {
    const y = m.zeroY - v * m.k
    ax.push(<line key={`l${idx}`} x1={m.ix0} y1={y.toFixed(1)} x2={m.ix1} y2={y.toFixed(1)} stroke="#eef2f7" />)
    ax.push(
      <text key={`t${idx}`} x={m.ix0 - 5} y={(y + 3).toFixed(1)} textAnchor="end" fontSize={10.5} fill="#94a3b8">
        {Math.round(v)}
      </text>,
    )
  })
  ax.push(
    <text key="dn" x={m.ix0 - 5} y={(m.zeroY + m.maxDn * m.k + 3).toFixed(1)} textAnchor="end" fontSize={10.5} fill="#94a3b8">
      −{Math.round(m.maxDn)}
    </text>,
  )

  const socAx: ReactNode[] = []
  if (m.has.socopt || m.has.socfact) {
    ;[0, 50, 100].forEach((p) => {
      socAx.push(
        <text key={p} x={m.ix1 + 6} y={(m.ySoc(p) + 3).toFixed(1)} textAnchor="start" fontSize={10.5} fill={C.socOpt}>
          {p}%
        </text>,
      )
    })
  }

  const legendItems: Array<{ key: SeriesKey; node: ReactNode }> = []
  if (m.has.load) legendItems.push({ key: 's-load', node: <><i style={{ background: C.disLoad }} />Розряд → споживання</> })
  if (m.has.exp) legendItems.push({ key: 's-exp', node: <><i style={{ background: C.disGrid }} />Розряд → експорт</> })
  if (m.has.sun) legendItems.push({ key: 's-sun', node: <><i style={{ background: C.chgPv }} />Заряд від сонця</> })
  if (m.has.grid) legendItems.push({ key: 's-grid', node: <><i style={{ background: C.chgGrid }} />Заряд від мережі</> })
  if (m.has.socopt) legendItems.push({ key: 's-socopt', node: <><i style={{ borderTop: `2px solid ${C.socOpt}`, background: 'none', height: 0 }} />SOC оптимум</> })
  if (m.has.socfact) legendItems.push({ key: 's-socfact', node: <><i style={{ borderTop: `2px dashed ${C.socFact}`, background: 'none', height: 0 }} />SOC факт</> })
  if (m.has.fact) legendItems.push({ key: 's-fact', node: <><i style={{ borderTop: `2px dashed ${C.fact}`, background: 'none', height: 0 }} />Факт УЗЕ</> })
  legendItems.push({ key: 's-rdn', node: <><i style={{ background: C.rdn, opacity: 0.5 }} />Ціна РДН</> })

  const optS = m.optS
  const factS = m.factS

  return (
    <div className="uze-layout">
      <div className="uze-left">
        <div className="uze-waterfall">
          <div className="uze-wf-title">Звідки {fmtUah(optS?.effect || 0)} оптимуму (собівартість врахована)</div>
          <div className="uze-wf-row pos"><span>Експорт УЗЕ → мережу</span><strong>{fmtUah(optS?.export_val || 0)}</strong></div>
          <div className="uze-wf-row pos"><span>УЗЕ → споживання</span><strong>{fmtUah(optS?.load_val || 0)}</strong></div>
          <div className="uze-wf-row neg"><span>Собівартість сонця (втрачений денний експорт)</span><strong>{fmtUah(-(optS?.charge_pv_cost || 0))}</strong></div>
          {optS?.grid_cost ? <div className="uze-wf-row neg"><span>Заряд з мережі</span><strong>{fmtUah(-(optS.grid_cost || 0))}</strong></div> : null}
          <div className="uze-wf-row neg"><span>Знос батареї</span><strong>{fmtUah(-(optS?.degradation || 0))}</strong></div>
          <div className="uze-wf-row total"><span>Чистий оптимум</span><strong>{fmtUah(optS?.effect || 0)}</strong></div>
        </div>
        <div className="uze-compare-box">
          <div><span className="lbl">Факт</span><strong className="fact">{fmtUah(factS?.effect || 0)}</strong></div>
          <div><span className="lbl">Оптимум</span><strong className="opt">{fmtUah(optS?.effect || 0)}</strong></div>
          <div><span className="lbl">Резерв</span><strong className="bad">{fmtUah(m.reserve)}</strong></div>
          <div><span className="lbl">Захоплено</span><strong>{m.captured}%</strong></div>
        </div>
        <p className="uze-chart-foot">
          Заряд за цикл: {fmtN(optS?.charge_pv_kwh || 0)} кВт·год з СЕС
          {optS?.charge_grid_kwh ? ` + ${fmtN(optS.charge_grid_kwh)} з мережі` : ''}.
          {' '}Розряд: {fmtN(optS?.discharge_kwh || 0)} кВт·год. Ліміт {fmtN(m.pwr)} кВт.
        </p>
      </div>
      <div className="uze-right">
        <div className="uze-chart-canvas" ref={canvasRef}>
          <svg viewBox={`0 0 ${m.W} ${m.H}`} role="img" aria-label="Погодинний оптимальний розряд/заряд УЗЕ">
            <text x={m.ix0 - 5} y={m.iy0 - 8} textAnchor="start" fontSize={10} fill="#94a3b8">кВт·год</text>
            {m.has.socopt || m.has.socfact ? (
              <text x={m.ix1 + 6} y={m.iy0 - 8} textAnchor="end" fontSize={10} fill={C.socOpt}>SOC</text>
            ) : null}
            <g style={{ display: vis('s-rdn') ? undefined : 'none' }}>{rdnCols}</g>
            {ax}
            {socAx}
            <line x1={m.ix0} y1={m.zeroY} x2={m.ix1} y2={m.zeroY} stroke={C.zero} strokeWidth={1} />
            <g style={{ display: vis('s-load') ? undefined : 'none' }}>{gLoad}</g>
            <g style={{ display: vis('s-exp') ? undefined : 'none' }}>{gExp}</g>
            <g style={{ display: vis('s-sun') ? undefined : 'none' }}>{gSun}</g>
            <g style={{ display: vis('s-grid') ? undefined : 'none' }}>{gGrid}</g>
            {m.has.fact ? (
              <polyline
                style={{ display: vis('s-fact') ? undefined : 'none' }}
                points={m.linePts(m.fEss, (v) => m.zeroY - v * m.k)}
                fill="none"
                stroke={C.fact}
                strokeWidth={1.6}
                strokeDasharray="5 3"
                opacity={0.85}
              />
            ) : null}
            {m.has.socfact ? (
              <polyline
                style={{ display: vis('s-socfact') ? undefined : 'none' }}
                points={m.socLine(m.socF, m.socFactStart)}
                fill="none"
                stroke={C.socFact}
                strokeWidth={1.6}
                strokeDasharray="3 2"
              />
            ) : null}
            {m.has.socopt ? (
              <polyline
                style={{ display: vis('s-socopt') ? undefined : 'none' }}
                points={m.socLine(m.socO, m.socOptStart)}
                fill="none"
                stroke={C.socOpt}
                strokeWidth={2}
              />
            ) : null}
            <g>{money}</g>
            {xlab}
            {hoverRects}
          </svg>
          {hover != null && m.tips[hover] ? (
            <div className="uze-tip" style={{ left: pos.x, top: pos.y }}>
              <div className="t-head">{m.tips[hover].head}</div>
              {m.tips[hover].rows.map((row, ri) => (
                <div className={`t-row${row.total ? ' total' : ''}`} key={ri}>
                  <span>
                    {row.swatch ? <i style={{ background: row.swatch }} /> : null}
                    {row.label}
                  </span>
                  <b>{row.value}</b>
                </div>
              ))}
            </div>
          ) : null}
        </div>
        <ul className="uze-chart-legend">
          {legendItems.map((li) => (
            <li key={li.key} className={hidden.has(li.key) ? 'off' : undefined} onClick={() => toggle(li.key)}>
              <span>{li.node}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

// UzeCyclesAccordion is the outer/inner accordion (§1.3): an outer toggle
// that reveals the list of significant cycles, each a per-day row that
// lazily renders its chart when expanded.
export function UzeCyclesAccordion({ cycles }: { cycles: EconomicsUzeCycle[] }) {
  const [groupOpen, setGroupOpen] = useState(false)
  const [open, setOpen] = useState<number | null>(null)
  if (!cycles.length) return null
  const n = cycles.length
  const reserves = cycles.map((c) => c.reserve_uah)
  const minReserve = Math.min(...reserves)
  const maxReserve = Math.max(...reserves)
  const meta = groupOpen
    ? `${n} циклів · натисніть, щоб згорнути`
    : `резерв від ${fmtUah(minReserve)} до ${fmtUah(maxReserve)} · натисніть, щоб розгорнути`
  return (
    <div className={`uze-acc-group${groupOpen ? ' open' : ''}`}>
      <button
        type="button"
        className="uze-acc-group-head"
        aria-expanded={groupOpen}
        onClick={() => setGroupOpen((v) => !v)}
      >
        <span className="uze-acc-group-title">
          {groupOpen ? `Деталізація по днях — ${n} циклів` : `Показати деталізацію по днях — ${n} циклів`}
        </span>
        <span className="uze-acc-group-meta">{meta}</span>
        <span className="uze-acc-group-arrow" aria-hidden="true">▾</span>
      </button>
      {groupOpen ? (
        <div className="uze-acc-group-body">
          {cycles.map((cyc, idx) => {
            const isOpen = open === idx
            return (
              <div key={cyc.start_date + cyc.label} className={`uze-acc-item${isOpen ? ' open' : ''}`}>
                <button
                  type="button"
                  className="uze-acc-head"
                  aria-expanded={isOpen}
                  onClick={() => setOpen(isOpen ? null : idx)}
                >
                  <span className="uze-acc-caret">{isOpen ? '▾' : '▸'}</span>
                  <span className="uze-acc-label">{cyc.label}</span>
                  <span className="uze-acc-metrics">
                    <span>опт {fmtUah(cyc.opt_effect_uah)}</span>
                    <span className="good">факт {fmtUah(cyc.actual_effect_uah)}</span>
                    <span className="bad">резерв {fmtUah(cyc.reserve_uah)}</span>
                  </span>
                </button>
                {isOpen ? (
                  <div className="uze-acc-body">
                    <CycleChartSvg chart={cyc.chart} />
                  </div>
                ) : null}
              </div>
            )
          })}
        </div>
      ) : null}
    </div>
  )
}
