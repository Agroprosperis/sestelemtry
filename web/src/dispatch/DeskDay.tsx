// DeskDay — a past day on the desk, read only: the measured fact next to
// the plan the edge was following (the newest publication before each
// hour, dispatch_runs). No editing bands; hovering shows the hour.

import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { PeriodPicker } from '../dashboard/components/PeriodPicker'
import { buildChartSvg, type ChartLayout, type SeriesToggles } from './chartSvg'
import { fetchDeskDay, type DayResponse, type DeskHour } from './dispatchClient'
import { Inspector } from './Inspector'
import { EMPTY_POINT, dateFromISO, flowMeta, fmt, hasValue, historyPoints, isoFromDate, makeClock, planOverlay } from './model'

type Props = {
  site: string
  date: string
  maxDate: string
  timezone: string
  onDate: (date: string) => void
  onClose: () => void
  onOpenReport?: (date: string) => void
}

const SERIES: [keyof SeriesToggles, string, string][] = [
  ['price', 'd-price', 'РДН'],
  ['pv', 'd-pv', 'СЕС'],
  ['load', 'd-load', 'Споживання'],
  ['grid', 'd-grid', 'Мережа PCC'],
  ['bess', 'd-bess', 'УЗЕ'],
  ['soc', 'd-soc', 'SOC'],
  ['plan', 'd-plan', 'План'],
]

const dayTitle = (date: string) =>
  new Intl.DateTimeFormat('uk-UA', { day: 'numeric', month: 'long', year: 'numeric' }).format(new Date(date + 'T12:00:00'))

const energy = (xs: (number | null | undefined)[], sign: 1 | -1) =>
  xs.reduce<number>((n, v) => n + (hasValue(v) ? Math.max(0, sign * v) : 0), 0)

export function DeskDay({ site, date, maxDate, timezone, onDate, onClose, onOpenReport }: Props) {
  const [loaded, setLoaded] = useState<{ date: string; day: DayResponse } | null>(null)
  const [failed, setFailed] = useState<{ date: string; text: string } | null>(null)
  const [series, setSeries] = useState<SeriesToggles>({
    price: true,
    pv: true,
    load: true,
    grid: true,
    bess: true,
    soc: true,
    before: false,
    plan: true,
  })
  const [layout, setLayout] = useState<'split' | 'combined'>('split')
  const [inspectAt, setInspectAt] = useState<{ date: string; i: number } | null>(null)
  const [width, setWidth] = useState(900)
  const chartRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    fetchDeskDay(site, date, ctrl.signal)
      .then((day) => setLoaded({ date, day }))
      .catch((e) => {
        if (!ctrl.signal.aborted) setFailed({ date, text: String(e) })
      })
    return () => ctrl.abort()
  }, [site, date])

  const day = loaded && loaded.date === date ? loaded.day : null
  const error = failed && failed.date === date ? failed.text : ''
  const n = day?.hours.length ?? 0
  useEffect(() => {
    const el = chartRef.current
    if (!el) return
    const ro = new ResizeObserver((entries) => {
      const w = entries[0].contentRect.width
      setWidth((prev) => (Math.abs(prev - w) > 1 ? w : prev))
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [n])

  const heading = (
    <div className="d-heading">
      <div>
        <h2>План і факт · {dayTitle(date)}</h2>
        <p className="d-small">Лише перегляд: факт із телеметрії, план — остання публікація до початку кожної години.</p>
      </div>
      <div className="d-row">
        <PeriodPicker
          preset="day"
          anchor={dateFromISO(date)}
          onChange={(d) => {
            const iso = isoFromDate(d)
            if (iso >= maxDate) onClose()
            else onDate(iso)
          }}
        />
        {onOpenReport && (
          <button type="button" onClick={() => onOpenReport(date)}>
            Звіт за день
          </button>
        )}
        <button type="button" onClick={onClose}>
          ← До пульта
        </button>
      </div>
    </div>
  )

  if (error) {
    return (
      <>
        {heading}
        <div className="ctl-notice err">{error}</div>
      </>
    )
  }
  if (!day) {
    return (
      <>
        {heading}
        <div className="ctl-placeholder">Завантаження дня…</div>
      </>
    )
  }
  if (n === 0) {
    return (
      <>
        {heading}
        <div className="ctl-placeholder">За цей день немає даних.</div>
      </>
    )
  }

  const hours: DeskHour[] = day.hours.map((h, k) => ({
    ts: h.ts,
    offset: k - n,
    rdn_uah_per_kwh: h.rdn_uah_per_kwh,
    buy_uah_per_kwh: h.buy_uah_per_kwh,
    sell_uah_per_kwh: h.sell_uah_per_kwh,
    pv_kw: 0,
    fact: h.fact,
    plan: h.plan,
  }))
  const past = historyPoints(hours)
  const at = (i: number) => hours[i + n]
  const clock = makeClock(new Date(Date.parse(hours[n - 1].ts) + 3_600_000).toISOString(), timezone)
  const inspect = inspectAt && inspectAt.date === date ? inspectAt.i : -n
  const pvAt = (i: number) => at(i)?.fact?.pv_kw ?? null
  const loadAt = (i: number) => at(i)?.fact?.load_kw ?? null
  const priceAt = (i: number) => at(i)?.rdn_uah_per_kwh ?? null
  const pointAt = (i: number) => past[i + n] ?? EMPTY_POINT
  const chart = buildChartSvg({
    width,
    clock,
    first: -n,
    count: n,
    series,
    layout,
    values: true,
    reserve: day.site.soc_min_pct,
    start: 0,
    end: 0,
    inspectHour: inspect,
    editable: false,
    pvAt,
    loadAt,
    priceAt,
    previewAt: pointAt,
    baselineAt: () => EMPTY_POINT,
    planAt: (i) => planOverlay(at(i)?.plan),
    actions: [],
    modes: [],
  })

  const hitIndex = (l: ChartLayout, clientX: number): number => {
    const el = chartRef.current
    if (!el) return inspect
    const r = el.getBoundingClientRect()
    return Math.max(l.first, Math.min(l.first + l.count - 1, l.first + Math.floor((clientX - r.left - l.left) / l.step)))
  }
  const track = (e: ReactPointerEvent) => {
    if (!(e.target as Element).closest('[data-brush]')) return
    const i = hitIndex(chart, e.clientX)
    if (i !== inspect) setInspectAt({ date, i })
  }

  const factEss = hours.map((h) => h.fact?.ess_kw)
  const planEss = hours.map((h) => h.plan?.ess_kw)
  const planned = hours.filter((h) => h.plan).length

  return (
    <>
      {heading}
      <section className="d-workspace d-day" aria-label="План і факт за день">
        <div className="d-context">
          <div className="d-chart-head">
            <h3>Факт і план УЗЕ</h3>
            <div className="d-chart-modes" role="group" aria-label="Компонування графіка">
              {(['split', 'combined'] as const).map((l) => (
                <button key={l} type="button" aria-pressed={layout === l} onClick={() => setLayout(l)}>
                  {l === 'split' ? 'Розділено' : 'Разом'}
                </button>
              ))}
            </div>
          </div>
          <div className="d-series-controls" role="group" aria-label="Показники на графіку">
            {SERIES.map(([key, cls, label]) => (
              <button
                key={key}
                type="button"
                className="d-key"
                aria-pressed={series[key]}
                onClick={() => setSeries((s) => ({ ...s, [key]: !s[key] }))}
              >
                <b className={cls} />
                {label}
              </button>
            ))}
          </div>
          <div className="d-day-summary">
            <span>
              Факт УЗЕ · розряд <strong>{fmt(energy(factEss, 1))}</strong> · заряд <strong>{fmt(energy(factEss, -1))}</strong> кВт·год
            </span>
            {planned > 0 ? (
              <span>
                План · розряд <strong>{fmt(energy(planEss, 1))}</strong> · заряд <strong>{fmt(energy(planEss, -1))}</strong> кВт·год · план
                діяв {planned} з {n} год
              </span>
            ) : (
              <span>Плану на цей день не було — edge працював на самоспоживання.</span>
            )}
          </div>
          <div
            ref={chartRef}
            className="d-chart"
            role="img"
            aria-label={'План і факт УЗЕ за ' + dayTitle(date)}
            onPointerDown={track}
            onPointerMove={track}
            dangerouslySetInnerHTML={{ __html: chart.svg }}
          />
          {series.grid && (
            <div className="d-grid-legend">
              <span>
                <b />
                Мережа PCC · весь об’єкт, кВт
              </span>
              <span>вище нуля — імпорт із мережі</span>
              <span>нижче нуля — експорт у мережу</span>
            </div>
          )}
          <div className="d-chart-note">
            {series.bess && (
              <span className="d-flow-legend" aria-label="Джерела заряду та призначення розряду УЗЕ">
                {Object.entries(flowMeta).map(([key, meta]) => (
                  <span className="d-key" key={key}>
                    <b style={{ background: meta.color }} />
                    {meta.name}
                  </span>
                ))}
              </span>
            )}
            <span>Суцільні — факт · штрихові — план</span>
          </div>
          <Inspector
            clock={clock}
            i={inspect}
            point={pointAt(inspect)}
            pv={pvAt(inspect)}
            load={loadAt(inspect)}
            price={priceAt(inspect)}
            buy={null}
            sell={null}
            plan={planOverlay(at(inspect)?.plan)}
            state={{ label: at(inspect)?.plan ? 'план і факт' : 'факт · плану не було', detail: '' }}
            series={series}
          />
        </div>
      </section>
    </>
  )
}
