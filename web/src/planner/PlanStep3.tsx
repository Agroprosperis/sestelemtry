import { useMemo, useState } from 'react'
import type { PlanDayEffect, PlanPreview, PlanPreviewHour } from './plannerClient'

// Step-3 visuals ported 1:1 from the ems-spec mockup (cloud_console
// `render()` + uze-* styles): the custom SVG dispatch chart with the
// RDN backdrop and price-tier pills, money labels over discharge bars,
// the boundary-point SOC line, «решта сьогодні / ЗАВТРА» overlays and
// the left panel (waterfall rows + compare box) instead of a bar chart.

// Colors 1:1 from the mockup's render() palette (cloud_console.html).
const C = {
  disLoad: '#16a34a',
  disGrid: '#ea580c',
  chgPv: '#facc15',
  chgGrid: '#9ca3af',
  rdn: '#3b82f6',
  socOpt: '#9333ea',
  money: '#166534',
  zero: '#475569',
}

function rdnTier(p: number): { fill: string; opacity: number; text: string } {
  if (p >= 8) return { fill: '#fecaca', opacity: 0.72, text: '#991b1b' }
  if (p >= 4) return { fill: '#fef08a', opacity: 0.55, text: '#854d0e' }
  if (p >= 1.5) return { fill: '#bfdbfe', opacity: 0.5, text: '#1e40af' }
  return { fill: '#dbeafe', opacity: 0.45, text: '#1d4ed8' }
}

const fmtN = (v: number) => Math.round(v).toLocaleString('uk-UA')
const fmtUahSigned = (v: number) =>
  (v < 0 ? '−' : '') + Math.abs(Math.round(v)).toLocaleString('uk-UA') + ' ₴'

type Series = 'load' | 'exp' | 'sun' | 'grid' | 'soc' | 'reserve' | 'rdn'

// Розряд ділиться на «в споживання» (все, крім експорту) та «в мережу».
const disToLoad = (h: PlanPreviewHour) => h.discharge_kwh - (h.discharge_grid_kwh ?? 0)
const disToGrid = (h: PlanPreviewHour) => h.discharge_grid_kwh ?? 0

type TipState = { i: number; x: number; y: number } | null

export function PlanChartSvg({
  preview,
  onHourClick,
}: {
  preview: PlanPreview
  onHourClick?: (h: PlanPreviewHour) => void
}) {
  const [hidden, setHidden] = useState<Set<Series>>(new Set())
  const [tip, setTip] = useState<TipState>(null)

  const hours = preview.hours
  const n = hours.length
  const hourFmt = useMemo(
    () => new Intl.DateTimeFormat('uk-UA', { timeZone: preview.timezone, hour: '2-digit', hour12: false }),
    [preview.timezone],
  )
  const dayFmt = useMemo(
    () =>
      new Intl.DateTimeFormat('uk-UA', { timeZone: preview.timezone, day: '2-digit', month: '2-digit' }),
    [preview.timezone],
  )

  if (n === 0) return null

  const labelOf = (h: PlanPreviewHour) => hourFmt.format(new Date(h.ts))
  // Tooltip head 1:1 with the mockup's horizonLabel: "DD.MM HH".
  const headOf = (h: PlanPreviewHour) => `${dayFmt.format(new Date(h.ts))} ${labelOf(h)}`
  const todayRest = hours.filter((h) => !h.tomorrow).length
  const income = (h: PlanPreviewHour) =>
    disToLoad(h) * h.import_uah_per_kwh + disToGrid(h) * h.export_uah_per_kwh
  const gridCost = (h: PlanPreviewHour) => h.charge_grid_kwh * h.import_uah_per_kwh

  // Geometry per the mockup: zero line at 60% of the plot height,
  // independent up/down scaling, RDN columns behind everything.
  const W = 940
  const H = 360
  const mt = 30
  const mr = 44
  const mb = 58
  const ml = 40
  const ix0 = ml
  const ix1 = W - mr
  const iy0 = mt
  const iy1 = H - mb
  const iw = ix1 - ix0
  const ih = iy1 - iy0
  const zeroY = iy0 + ih * 0.6

  let maxUp = 1
  let maxDn = 1
  let rdnMax = 1
  for (const h of hours) {
    maxUp = Math.max(maxUp, h.discharge_kwh)
    maxDn = Math.max(maxDn, h.charge_pv_kwh + h.charge_grid_kwh)
    rdnMax = Math.max(rdnMax, h.rdn_uah_per_kwh ?? 0)
  }
  const k = Math.min((zeroY - iy0) / maxUp, (iy1 - zeroY) / maxDn)
  const slot = iw / n
  const cx = (i: number) => ix0 + (i + 0.5) * slot
  const ySoc = (p: number) => iy1 - (p / 100) * ih
  const dense = n > 30
  const bw = Math.min(26, slot * 0.62)
  const show = (s: Series) => !hidden.has(s)

  const toggle = (s: Series) =>
    setHidden((prev) => {
      const next = new Set(prev)
      if (next.has(s)) next.delete(s)
      else next.add(s)
      return next
    })

  const texts: React.ReactNode[] = []

  // RDN backdrop + price pills under the axis.
  if (show('rdn')) {
    hours.forEach((h, i) => {
      const price = h.rdn_uah_per_kwh
      if (price == null) return
      const x = ix0 + i * slot + 1
      const bh = (price / rdnMax) * ih
      texts.push(
        <rect key={'r' + i} x={x} y={iy1 - bh} width={slot - 2} height={bh} fill={C.rdn} fillOpacity={0.13} />,
      )
      if (!dense || i % 2 === 0) {
        const tier = rdnTier(price)
        const lw = dense ? slot * 2 - 2 : slot - 2
        texts.push(
          <g key={'rl' + i}>
            <rect x={x} y={iy1 + 5} width={lw} height={14} fill={tier.fill} fillOpacity={tier.opacity} rx={3} />
            <text
              x={ix0 + (i + (dense ? 1 : 0.5)) * slot}
              y={iy1 + 16}
              textAnchor="middle"
              fontSize={dense ? 8.5 : 9}
              fill={tier.text}
              fontWeight={price >= 8 ? 700 : 500}
            >
              {price.toFixed(1)}
            </text>
          </g>,
        )
      }
    })
  }

  // «Решта сьогодні» amber zone + section titles.
  if (todayRest > 0 && todayRest < n) {
    texts.push(
      <g key="zones">
        <rect x={ix0} y={iy0} width={todayRest * slot} height={ih} fill="#f59e0b" fillOpacity={0.06} />
        <text x={ix0 + (todayRest * slot) / 2} y={iy0 + 10} textAnchor="middle" fontSize={9} fill="#b45309">
          решта сьогодні · P&L сьогодні
        </text>
        <text
          x={ix0 + (todayRest + (n - todayRest) / 2) * slot}
          y={iy0 + 10}
          textAnchor="middle"
          fontSize={9}
          fontWeight={700}
          fill="#7c3aed"
        >
          ЗАВТРА · очікуваний ефект
        </text>
        <line
          x1={ix0 + todayRest * slot}
          y1={iy0 - 8}
          x2={ix0 + todayRest * slot}
          y2={iy1}
          stroke="#7c3aed"
          strokeWidth={1.5}
          strokeDasharray="4 3"
        />
        <text x={ix0 + todayRest * slot + 3} y={iy0 - 1} fontSize={10} fontWeight={700} fill="#7c3aed">
          завтра
        </text>
      </g>,
    )
  }
  texts.push(
    <g key="now">
      <line x1={ix0} y1={iy0 - 8} x2={ix0} y2={iy1} stroke="#16a34a" strokeWidth={1.5} />
      <text x={ix0 + 3} y={iy0 - 1} fontSize={10} fontWeight={700} fill="#16a34a">
        зараз
      </text>
    </g>,
  )

  // Zero line goes under the bars (mockup draws it before the groups).
  texts.push(<line key="zero" x1={ix0} y1={zeroY} x2={ix1} y2={zeroY} stroke={C.zero} strokeWidth={1} />)

  // Axis gridlines and scale labels.
  ;[maxUp, maxUp / 2].forEach((v, idx) => {
    const y = zeroY - v * k
    texts.push(
      <g key={'ax' + idx}>
        <line x1={ix0} y1={y} x2={ix1} y2={y} stroke="#eef2f7" />
        <text x={ix0 - 5} y={y + 3} textAnchor="end" fontSize={10.5} fill="#94a3b8">
          {Math.round(v)}
        </text>
      </g>,
    )
  })
  texts.push(
    <g key="ax-labels">
      <text x={ix0 - 5} y={zeroY + maxDn * k + 3} textAnchor="end" fontSize={10.5} fill="#94a3b8">
        −{Math.round(maxDn)}
      </text>
      <text x={ix0 - 5} y={iy0 - 8} textAnchor="start" fontSize={10} fill="#94a3b8">
        кВт·год
      </text>
      <text x={ix1 + 6} y={iy0 - 8} textAnchor="end" fontSize={10} fill={C.socOpt}>
        SOC
      </text>
      <text x={ix0 - 5} y={iy1 + 16} textAnchor="end" fontSize={8.5} fill="#1d4ed8" opacity={0.8}>
        РДН
      </text>
      <text x={ix0 - 5} y={iy1 + 36} textAnchor="end" fontSize={9} fill="#334155" fontWeight={600}>
        год
      </text>
    </g>,
  )
  // SOC axis % labels are not part of the legend toggle in the mockup.
  ;[0, 50, 100].forEach((p) =>
    texts.push(
      <text key={'soc' + p} x={ix1 + 6} y={ySoc(p) + 3} textAnchor="start" fontSize={10.5} fill={C.socOpt}>
        {p}%
      </text>,
    ),
  )

  // Bars + money labels + x labels.
  hours.forEach((h, i) => {
    const c = cx(i)
    const x = c - bw / 2
    // Stacking is computed independently of legend toggles (the mockup
    // hides an <g> without reflowing neighbours).
    let acc = 0
    if (disToLoad(h) > 0.5) {
      const hh = disToLoad(h) * k
      if (show('load')) {
        texts.push(
          <rect key={'d' + i} x={x} y={zeroY - acc - hh} width={bw} height={hh} fill={C.disLoad} rx={1} />,
        )
      }
      acc += hh
    }
    if (disToGrid(h) > 0.5) {
      const hh = disToGrid(h) * k
      if (show('exp')) {
        texts.push(
          <rect key={'dg' + i} x={x} y={zeroY - acc - hh} width={bw} height={hh} fill={C.disGrid} rx={1} />,
        )
      }
      acc += hh
    }
    let accd = 0
    if (h.charge_pv_kwh > 0.5) {
      const hh = h.charge_pv_kwh * k
      if (show('sun')) {
        texts.push(<rect key={'cp' + i} x={x} y={zeroY + accd} width={bw} height={hh} fill={C.chgPv} rx={1} />)
      }
      accd += hh
    }
    if (h.charge_grid_kwh > 0.5) {
      const hh = h.charge_grid_kwh * k
      if (show('grid')) {
        texts.push(<rect key={'cg' + i} x={x} y={zeroY + accd} width={bw} height={hh} fill={C.chgGrid} rx={1} />)
      }
      accd += hh
    }
    if (h.discharge_kwh > 3 && income(h) >= 5) {
      texts.push(
        <text
          key={'m' + i}
          x={c}
          y={zeroY - acc - 4}
          textAnchor="middle"
          fontSize={10.5}
          fontWeight={700}
          fill={C.money}
        >
          {fmtN(income(h))}
        </text>,
      )
    }
    const major = i % 3 === 0 || i === todayRest || i === 0
    texts.push(
      <text
        key={'x' + i}
        x={c}
        y={iy1 + 36}
        textAnchor="middle"
        fontSize={major ? 9 : 8}
        fill={h.tomorrow ? '#7c3aed' : '#334155'}
        fontWeight={major ? 700 : 500}
      >
        {labelOf(h)}
      </text>,
    )
  })

  // SOC line: points at hour boundaries (right edge), start point first.
  if (show('soc')) {
    const pts: string[] = [`${ix0},${ySoc(preview.params.start_soc_pct).toFixed(1)}`]
    hours.forEach((h, i) => pts.push(`${(ix0 + (i + 1) * slot).toFixed(1)},${ySoc(h.soc_end_pct).toFixed(1)}`))
    texts.push(
      <polyline key="socline" points={pts.join(' ')} fill="none" stroke={C.socOpt} strokeWidth={2} />,
    )
  }
  // Reserve line is its own legend series in the mockup ("Резерв SOC N%").
  if (show('reserve')) {
    const reserve = preview.params.soc_min_pct
    texts.push(
      <g key="reserve">
        <line
          x1={ix0}
          y1={ySoc(reserve)}
          x2={ix1}
          y2={ySoc(reserve)}
          stroke="#dc2626"
          strokeWidth={1.2}
          strokeDasharray="5 3"
          opacity={0.85}
        />
        <text x={ix1 + 4} y={ySoc(reserve) + 3} textAnchor="start" fontSize={9} fill="#dc2626">
          резерв {Math.round(reserve)}%
        </text>
      </g>,
    )
  }

  const tipHour = tip ? hours[tip.i] : null
  // Mirror the mockup's has.* checks: legend hides series with no data.
  const has = {
    load: hours.some((h) => disToLoad(h) > 0.5),
    exp: hours.some((h) => disToGrid(h) > 0.5),
    sun: hours.some((h) => h.charge_pv_kwh > 0.5),
    grid: hours.some((h) => h.charge_grid_kwh > 0.5),
  }
  // Legend items and wording 1:1 with the mockup (plan mode).
  const legend: { s: Series; label: string; swatch: React.CSSProperties }[] = []
  if (has.load) legend.push({ s: 'load', label: 'Розряд → споживання', swatch: { background: C.disLoad } })
  if (has.exp) legend.push({ s: 'exp', label: 'Розряд → експорт', swatch: { background: C.disGrid } })
  if (has.sun) legend.push({ s: 'sun', label: 'Заряд від сонця', swatch: { background: C.chgPv } })
  if (has.grid) legend.push({ s: 'grid', label: 'Заряд від мережі', swatch: { background: C.chgGrid } })
  legend.push({
    s: 'soc',
    label: 'SOC план',
    swatch: { borderTop: `2px solid ${C.socOpt}`, background: 'none', height: 0 },
  })
  legend.push({
    s: 'reserve',
    label: `Резерв SOC ${Math.round(preview.params.soc_min_pct)}%`,
    swatch: { borderTop: '2px dashed #dc2626', background: 'none', height: 0 },
  })
  legend.push({ s: 'rdn', label: 'Ціна РДН', swatch: { background: C.rdn, opacity: 0.5 } })

  return (
    <div className="uze-right">
      <p className="uze-horizon-note">
        Горизонт від «зараз» до кінця відомих РДН. Ліворуч поділу — решта сьогодні (контекст); правіше —{' '}
        <b>завтра</b>, за яке рахуємо очікуваний ефект
      </p>
      <div className="uze-chart-canvas" style={{ position: 'relative' }}>
        <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="План УЗЕ на добу">
          {texts}
          {hours.map((h, i) => (
            <rect
              key={'h' + i}
              x={ix0 + i * slot}
              y={iy0}
              width={slot}
              height={ih}
              fill="transparent"
              style={{ cursor: 'pointer' }}
              onMouseMove={(e) => {
                // Mouse-following tip, flip near the right edge — 1:1
                // with the mockup's mousemove handler.
                const host = (e.currentTarget.ownerSVGElement?.parentElement ?? null) as HTMLElement | null
                const rect = host?.getBoundingClientRect()
                if (!rect) return
                let px = e.clientX - rect.left + 14
                const py = e.clientY - rect.top + 12
                if (px + 190 > rect.width) px = e.clientX - rect.left - 200
                setTip({ i, x: px, y: py })
              }}
              onMouseLeave={() => setTip(null)}
              onClick={() => onHourClick?.(h)}
            />
          ))}
        </svg>
        {tip && tipHour && (
          <div
            className="uze-tip"
            style={{ display: 'block', left: tip.x, top: tip.y }}
          >
            <div className="t-head">{headOf(tipHour)}</div>
            {tipHour.rdn_uah_per_kwh != null && (
              <div className="t-row">
                <span>Ціна РДН</span>
                <b>{tipHour.rdn_uah_per_kwh.toFixed(2)} ₴</b>
              </div>
            )}
            <div className="t-row">
              <span>План load</span>
              <b>{fmtN(tipHour.load_kw)} кВт</b>
            </div>
            <div className="t-row">
              <span>Прогноз СЕС</span>
              <b>{fmtN(tipHour.pv_kw)} кВт</b>
            </div>
            {tipHour.discharge_kwh > 0.5 && (
              <>
                <div className="t-row">
                  <span>
                    <i style={{ background: C.disLoad }} />
                    На споживання
                  </span>
                  <b>{fmtN(disToLoad(tipHour))} кВт·год</b>
                </div>
                <div className="t-row">
                  <span>
                    <i style={{ background: C.disGrid }} />
                    На експорт
                  </span>
                  <b>{fmtN(disToGrid(tipHour))} кВт·год</b>
                </div>
                <div className="t-row total">
                  <span>Дохід розряду</span>
                  <b>{fmtUahSigned(income(tipHour))}</b>
                </div>
              </>
            )}
            {tipHour.charge_pv_kwh + tipHour.charge_grid_kwh > 0.5 && (
              <>
                {tipHour.charge_pv_kwh > 0.5 && (
                  <div className="t-row">
                    <span>
                      <i style={{ background: C.chgPv }} />
                      Заряд від сонця
                    </span>
                    <b>{fmtN(tipHour.charge_pv_kwh)} кВт·год</b>
                  </div>
                )}
                {tipHour.charge_grid_kwh > 0.5 && (
                  <div className="t-row">
                    <span>
                      <i style={{ background: C.chgGrid }} />
                      Заряд від мережі
                    </span>
                    <b>{fmtN(tipHour.charge_grid_kwh)} кВт·год</b>
                  </div>
                )}
                {gridCost(tipHour) > 0.5 && (
                  <div className="t-row total">
                    <span>Вартість заряду з мережі</span>
                    <b>{fmtUahSigned(-gridCost(tipHour))}</b>
                  </div>
                )}
              </>
            )}
            <div className="t-row">
              <span>SOC план</span>
              <b>{Math.round(tipHour.soc_end_pct)}%</b>
            </div>
          </div>
        )}
      </div>
      <ul className="uze-chart-legend">
        {legend.map((l) => (
          <li key={l.s} className={hidden.has(l.s) ? 'off' : ''} onClick={() => toggle(l.s)}>
            <span>
              <i style={l.swatch} />
              {l.label}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

// EffectPanel — the mockup's left column: waterfall rows, the compare
// box and the footnote (replaces the earlier recharts waterfall).
export function EffectPanel({
  day,
  dateLabel,
  preview,
}: {
  day: PlanDayEffect
  dateLabel: string
  preview: PlanPreview
}) {
  const loadSourceNote =
    preview.load_source === 'operator'
      ? 'Load: операторський план'
      : preview.load_source === 'operator_partial'
        ? 'Load: оператор + heuristic'
        : 'Load: heuristic (медіана 14 діб)'
  return (
    <div className="uze-left">
      <p className="uze-chart-foot" style={{ margin: 0, color: '#64748b' }}>
        {loadSourceNote} · ефект за добу <strong>завтра {dateLabel}</strong>
      </p>
      <div className="uze-waterfall">
        <div className="uze-wf-title">
          Очікуваний ефект за добу (завтра {dateLabel}): {fmtUahSigned(day.net_effect_uah)}
        </div>
        <div className="uze-wf-row pos">
          <span>УЗЕ → споживання (уникнений all-in імпорт)</span>
          <strong>{fmtUahSigned(day.ess_to_load_uah)}</strong>
        </div>
        {(day.ess_to_grid_uah ?? 0) > 0.5 && (
          <div className="uze-wf-row pos">
            <span>УЗЕ → експорт (пік РДН −5%)</span>
            <strong>{fmtUahSigned(day.ess_to_grid_uah)}</strong>
          </div>
        )}
        <div className="uze-wf-row neg">
          <span>Собівартість сонця (втрачений денний експорт)</span>
          <strong>{fmtUahSigned(-day.pv_charge_cost_uah)}</strong>
        </div>
        {day.grid_charge_cost_uah > 0.5 && (
          <div className="uze-wf-row neg">
            <span>Заряд з мережі (дешеві години)</span>
            <strong>{fmtUahSigned(-day.grid_charge_cost_uah)}</strong>
          </div>
        )}
        <div className="uze-wf-row neg">
          <span>Знос батареї</span>
          <strong>{fmtUahSigned(-day.degradation_uah)}</strong>
        </div>
        {Math.abs(day.soc_carry_uah) > 0.5 && (
          <div className={'uze-wf-row ' + (day.soc_carry_uah >= 0 ? 'pos' : 'neg')}>
            <span>
              Резерв SOC за добу ({Math.round(day.soc_open_pct)}% → {Math.round(day.soc_close_pct)}%, перенос на
              наст. добу за {day.shadow_price_uah.toFixed(2)} ₴)
            </span>
            <strong>{fmtUahSigned(day.soc_carry_uah)}</strong>
          </div>
        )}
        <div className="uze-wf-row total">
          <span>Чистий ефект за добу</span>
          <strong>{fmtUahSigned(day.net_effect_uah)}</strong>
        </div>
      </div>
      <div className="uze-compare-box">
        <div>
          <span className="lbl">Без УЗЕ-плану</span>
          <strong className="fact">{fmtUahSigned(day.baseline_cost_uah)}</strong>
        </div>
        <div>
          <span className="lbl">Імпорт з планом</span>
          <strong className="opt">{fmtUahSigned(day.plan_cost_uah)}</strong>
        </div>
        <div>
          <span className="lbl">Економія</span>
          <strong className="bad">{fmtUahSigned(Math.max(0, day.baseline_cost_uah - day.plan_cost_uah))}</strong>
        </div>
        <div>
          <span className="lbl">Заряд з мережі</span>
          <strong>{fmtN(day.charge_grid_kwh)} кВт·год</strong>
        </div>
      </div>
      <p className="uze-chart-foot">
        «Без плану» — прямий імпорт (load − СЕС). Розряд: {fmtN(day.ess_to_load_kwh + (day.ess_to_grid_kwh ?? 0))}{' '}
        кВт·год. Ліміт {fmtN(preview.params.power_kw)} кВт.
      </p>
    </div>
  )
}
