// DispatchDesk — the manual-control desk that replaces the three-step
// planner (ems-spec docs/specs/ems_manual_control_mvp.md, mockup
// ems-dispatch-desk-browser-v2.html, guide ems-demo-guide.md). The
// operator enters expected load, sets intents on hour intervals, checks
// the preview, stages a draft and confirms it; the server prices every
// version with the same LP the rolling publisher uses.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import './dispatch.css'
import { buildChartSvg, type ChartLayout, type ModeGroup, type SeriesToggles } from './chartSvg'
import { PeriodPicker } from '../dashboard/components/PeriodPicker'
import { ChartNotes, ChartToolbar, type ChartLayoutMode } from './ChartParts'
import { CommandFields, CommandHelp, CommandPreview, IntervalHead, LoadEditor } from './CommandEditor'
import { ConstraintsPanel } from './ConstraintsPanel'
import { DeskDay } from './DeskDay'
import { DeskFootnotes } from './DeskFootnotes'
import { HISTORY_LIMIT, cfgToForm, formToCfg, readDraft, writeDraft, type CfgForm, type Selection } from './deskDraft'
import { Inspector } from './Inspector'
import { ReviewPanel } from './ReviewPanel'
import { confirmDraft, fetchDeskState, previewDraft, type CommandType, type DeskState, type SimResult } from './dispatchClient'
import {
  EMPTY_POINT,
  FUTURE_HOURS,
  HISTORY_HOURS,
  actionGroups,
  buildPreview,
  clone,
  defaultParams,
  fmt,
  hasValue,
  historyPoints,
  hourPlanState,
  hoursBetween,
  isoFromDate,
  dateFromISO,
  localDate,
  makeClock,
  normalizeModel,
  planOverlay,
  pointFromResult,
  reviewGroups,
  same,
  shiftModel,
  timeLabel,
  type ChartPoint,
  type Clock,
  type DeskModel,
  type PlanOverlay,
} from './model'
import { commandOutcome } from './outcome'
import { commandError, constraintsError } from './validate'

// onOpenReport opens the economics day report for a date; absent when
// the user cannot read economics on the site.
type Props = { site: string; onOpenReport?: (date: string) => void }

const STATE_REFRESH_MS = 5 * 60_000
const PREVIEW_DEBOUNCE_MS = 250

export function DispatchDesk({ site, onOpenReport }: Props) {
  const [state, setState] = useState<DeskState | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [active, setActive] = useState<DeskModel | null>(null)
  const [working, setWorking] = useState<DeskModel | null>(null)
  const [history, setHistory] = useState<DeskModel[]>([])
  const [baseVersion, setBaseVersion] = useState(0)
  const [sel, setSel] = useState<Selection>({ start: 0, end: 1, anchor: 0, inspect: 0 })
  const [scenario, setScenario] = useState<CommandType>('cover')
  const [params, setParams] = useState<Record<CommandType, string>>(
    () => Object.fromEntries(Object.entries(defaultParams).map(([k, v]) => [k, String(v)])) as Record<CommandType, string>,
  )
  const [direction, setDirection] = useState<'charge' | 'discharge'>('discharge')
  const [form, setForm] = useState<CfgForm | null>(null)
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
  const [layout, setLayout] = useState<ChartLayoutMode>('split')
  const [reviewOpen, setReviewOpen] = useState(false)
  // An action's message stays until the operator touches the fields or
  // the selection again (the mockup overwrites it on the next render).
  const fieldsKey = JSON.stringify([scenario, params[scenario], direction, sel.start, sel.end, form, reviewOpen])
  const [statusState, setStatusState] = useState<{ text: string; key: string | null }>({
    text: 'Попередній перегляд · зміни ще не застосовано.',
    key: null,
  })
  const setStatus = useCallback((text: string) => setStatusState({ text, key: fieldsKey }), [fieldsKey])
  const status = statusState.text
  const [loadEdit, setLoadEdit] = useState<{ key: string; value: string; error: string } | null>(null)
  // Server results by model key; `last` is shown while a newer one is out.
  const [results, setResults] = useState<{ map: Record<string, SimResult>; last: string }>({ map: {}, last: '' })
  const [serverInvalid, setServerInvalid] = useState<{ key: string; text: string } | null>(null)
  const [confirmBusy, setConfirmBusy] = useState(false)
  const [confirmIssues, setConfirmIssues] = useState<string>('')
  const stateRef = useRef<DeskState | null>(null)
  const activeRef = useRef<DeskModel | null>(null)
  const workingRef = useRef<DeskModel | null>(null)
  useEffect(() => {
    stateRef.current = state
    activeRef.current = active
    workingRef.current = working
  }, [state, active, working])

  // --- load / refresh ----------------------------------------------------

  const applyState = useCallback(
    (next: DeskState, initial: boolean) => {
      const applied = normalizeModel(next.applied.model)
      const prev = stateRef.current
      setState(next)
      setActive(applied)
      setResults({ map: {}, last: '' })
      if (initial || !prev) {
        const saved = readDraft(site)
        if (saved && saved.base_version === next.applied.version) {
          const shift = hoursBetween(saved.start_hour, next.start_hour)
          const restored = shiftModel(normalizeModel(saved.working), shift)
          setWorking(restored)
          setHistory((saved.history ?? []).map((h) => shiftModel(normalizeModel(h), shift)))
          setForm(cfgToForm(restored.cfg))
          if (!same(restored, applied)) setStatusState({ text: 'Відновлено незбережену чернетку з цього браузера.', key: null })
        } else {
          if (saved) setNotice('Збережену чернетку скинуто: план змінився (версія ' + next.applied.version + ').')
          setWorking(clone(applied))
          setHistory([])
          setForm(cfgToForm(applied.cfg))
        }
        setBaseVersion(next.applied.version)
        return
      }
      // Periodic refresh: time moves on, and someone else may confirm.
      const shift = hoursBetween(prev.start_hour, next.start_hour)
      const oldActive = activeRef.current
      const oldWorking = workingRef.current
      if (next.applied.version !== prev.applied.version) {
        if (oldWorking && oldActive && !same(oldWorking, oldActive)) {
          setNotice(
            'План змінив ' + (next.applied.confirmed_by || 'інший користувач') + ' (версія ' + next.applied.version +
              '). Ваша чернетка базується на попередній версії — підтвердження буде відхилене, перенесіть зміни.',
          )
          if (shift > 0) {
            setWorking((w) => (w ? shiftModel(w, shift) : w))
            setHistory((hs) => hs.map((h) => shiftModel(h, shift)))
          }
        } else {
          setWorking(clone(applied))
          setHistory([])
          setForm(cfgToForm(applied.cfg))
          setBaseVersion(next.applied.version)
        }
      } else if (shift > 0) {
        setWorking((w) => (w ? shiftModel(w, shift) : w))
        setHistory((hs) => hs.map((h) => shiftModel(h, shift)))
      }
      if (shift > 0)
        setSel((s) => {
          const start = Math.max(0, s.start - shift)
          return { start, end: Math.max(start + 1, s.end - shift), anchor: start, inspect: start }
        })
    },
    [site],
  )

  // The parent keys the desk by site, so a site switch remounts it.
  useEffect(() => {
    let cancelled = false
    const load = (initial: boolean) =>
      fetchDeskState(site)
        .then((next) => {
          if (cancelled) return
          setError('')
          applyState(next, initial)
        })
        .catch((e) => {
          if (!cancelled) setError(String(e))
        })
    void load(true)
    const id = window.setInterval(() => void load(false), STATE_REFRESH_MS)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [site, applyState])

  useEffect(() => {
    if (!state || !working || !active) return
    writeDraft(
      site,
      same(working, active) && history.length === 0
        ? null
        : { start_hour: state.start_hour, base_version: baseVersion, working, history: history.slice(-HISTORY_LIMIT) },
    )
  }, [site, state, working, active, history, baseVersion])

  // --- derived model -----------------------------------------------------

  const clock: Clock | null = useMemo(() => (state ? makeClock(state.start_hour, state.timezone) : null), [state])
  const cfg = useMemo(() => (form ? formToCfg(form) : null), [form])
  const siteInfo = state?.site ?? null
  const paramRaw = params[scenario]
  const cfgError = siteInfo && cfg && form ? constraintsError(siteInfo, cfg, form.reserve, form.importCap) : ''
  const cmdError = siteInfo && cfg ? commandError(siteInfo, cfg, scenario, paramRaw, direction) : ''
  const localInvalid = cfgError || cmdError

  const previewModel = useMemo(() => {
    if (!working || !cfg) return null
    if (reviewOpen) return clone(working)
    return buildPreview(working, cfg, scenario, Number(paramRaw), direction, sel.start, sel.end)
  }, [working, cfg, reviewOpen, scenario, paramRaw, direction, sel.start, sel.end])
  const chartModel = localInvalid ? working : previewModel
  const modelKey = state && chartModel ? state.start_hour + '|' + JSON.stringify(chartModel) : ''
  const unchanged = !!chartModel && !!active && same(chartModel, active)
  const ready = unchanged ? state?.result : results.map[modelKey]

  useEffect(() => {
    if (!state || !chartModel || ready) return
    const key = modelKey
    const ctrl = new AbortController()
    const timer = window.setTimeout(() => {
      previewDraft(site, { ...chartModel, start_hour: state.start_hour, base_version: baseVersion }, ctrl.signal)
        .then((res) => {
          if (res.invalid) {
            setServerInvalid({ key, text: res.invalid })
            return
          }
          const result = res.result
          if (!result) return
          setResults((r) => {
            const map = Object.keys(r.map).length > 40 ? {} : { ...r.map }
            map[key] = result
            return { map, last: key }
          })
        })
        .catch((e) => {
          if (!ctrl.signal.aborted) setError(String(e))
        })
    }, PREVIEW_DEBOUNCE_MS)
    return () => {
      window.clearTimeout(timer)
      ctrl.abort()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelKey, site, baseVersion, !!ready])

  const invalid = localInvalid || (serverInvalid && serverInvalid.key === modelKey ? serverInvalid.text : '')
  const result = ready ?? results.map[results.last] ?? state?.result ?? null
  const stale = !ready

  // The load field follows the selection (the mockup's syncLoadEditor):
  // a typed value lives only until the selection or its loads change.
  const selectionLoads = working ? working.loads.slice(sel.start, sel.end) : []
  const uniformLoad = selectionLoads.every((v) => v === selectionLoads[0])
  const loadKey = sel.start + '-' + sel.end + '|' + JSON.stringify(selectionLoads)
  const loadEditing = loadEdit && loadEdit.key === loadKey ? loadEdit : null
  const loadInput = loadEditing
    ? loadEditing.value
    : uniformLoad && selectionLoads[0] !== null && selectionLoads[0] !== undefined
      ? String(selectionLoads[0])
      : ''
  const loadError = loadEditing?.error ?? ''
  const setLoadInput = (value: string) => setLoadEdit({ key: loadKey, value, error: '' })
  const setLoadError = (error: string) => setLoadEdit({ key: loadKey, value: loadInput, error })

  // --- actions -----------------------------------------------------------

  const pushHistory = (prev: DeskModel) => setHistory((h) => [...h, clone(prev)].slice(-HISTORY_LIMIT))

  const select = (i: number, shift: boolean) =>
    setSel((s) => {
      const anchor = shift ? s.anchor : i
      return { anchor, inspect: i, start: Math.min(anchor, i), end: Math.max(anchor, i) + 1 }
    })

  const stageLoad = (clear: boolean) => {
    if (!working || !clock) return
    const raw = loadInput.trim()
    const value = clear ? null : Number(raw.replace(',', '.'))
    if (!clear && (raw === '' || !Number.isFinite(value) || (value as number) < 0)) {
      setLoadError('Введіть споживання у кВт: 0 або додатне число.')
      return
    }
    const next = clone(working)
    for (let i = sel.start; i < sel.end; i++) next.loads[i] = value
    if (!same(next, working)) {
      pushHistory(working)
      setWorking(next)
    }
    setStatus(
      'Споживання ' + timeLabel(clock, sel.start) + '–' + timeLabel(clock, sel.end) + ' ' + (clear ? 'очищено' : 'додано') +
        ' у чернетку. Команду УЗЕ не змінено.',
    )
  }

  const stage = () => {
    if (invalid || !working || !previewModel || !clock) return
    if (!same(working, previewModel)) {
      pushHistory(working)
      setWorking(clone(previewModel))
    }
    setStatus('У чернетці: ' + timeLabel(clock, sel.start) + '–' + timeLabel(clock, sel.end) + '. Можна додавати інші команди або переглянути чернетку.')
  }

  const stageConstraints = () => {
    if (cfgError || !working || !cfg) return
    if (!same(cfg, working.cfg)) {
      pushHistory(working)
      setWorking({ ...clone(working), cfg: clone(cfg) })
    }
    setReviewOpen(true)
    setStatus('Обмеження додано у чернетку. Команди в годинах збережені.')
  }

  const undo = () => {
    if (!history.length) return
    const prev = history[history.length - 1]
    setHistory((h) => h.slice(0, -1))
    setWorking(prev)
    setForm(cfgToForm(prev.cfg))
    setStatus('Зміну чернетки скасовано. Графік показує поточний попередній перегляд.')
  }

  const openReview = () => {
    if (working) setForm(cfgToForm(working.cfg))
    setConfirmIssues('')
    setReviewOpen(true)
  }
  const closeReview = () => {
    if (working) setForm(cfgToForm(working.cfg))
    setReviewOpen(false)
  }

  const reviewReserve = (raw: string) => {
    if (!form || !working || !siteInfo) return
    setForm({ ...form, reserve: raw })
    const n = Number(raw)
    const ok = raw.trim() !== '' && Number.isFinite(n) && n >= siteInfo.soc_min_pct && n <= siteInfo.soc_max_pct
    if (ok && n !== working.cfg.reserve_pct) {
      pushHistory(working)
      setWorking({ ...clone(working), cfg: { ...working.cfg, reserve_pct: n } })
    }
  }

  const reviewBlockExport = (on: boolean) => {
    if (!working) return
    pushHistory(working)
    const next = { ...clone(working), cfg: { ...working.cfg, block_export: on } }
    setWorking(next)
    setForm(cfgToForm(next.cfg))
  }

  const editIssue = (i: number) => {
    if (!working) return
    const cmd = working.commands[i]
    let start = i
    let end = i + 1
    if (cmd) {
      while (start > 0 && same(working.commands[start - 1], cmd)) start--
      while (end < FUTURE_HOURS && same(working.commands[end], cmd)) end++
    }
    setSel({ start, end, anchor: start, inspect: start })
    setForm(cfgToForm(working.cfg))
    setScenario(cmd ? cmd.type : 'auto')
    setDirection(cmd?.direction ?? 'discharge')
    if (cmd) setParams((p) => ({ ...p, [cmd.type]: String(cmd.value) }))
    setReviewOpen(false)
  }

  const confirm = async () => {
    if (!state || !working || !active || !clock) return
    setConfirmBusy(true)
    setConfirmIssues('')
    try {
      const res = await confirmDraft(site, { ...working, start_hour: state.start_hour, base_version: baseVersion })
      if (res.status === 200) {
        writeDraft(site, null)
        setHistory([])
        setReviewOpen(false)
        setNotice(
          'Застосовано · версія ' + res.body.version + '. План опубліковано на edge' +
            (res.body.publish?.manifest_id ? ' (' + res.body.publish.manifest_id + ')' : '') + ', режим shadow — без запису в SmartLogger.',
        )
        const next = await fetchDeskState(site)
        setResults({ map: {}, last: '' })
        setState(next)
        const applied = normalizeModel(next.applied.model)
        setActive(applied)
        setWorking(clone(applied))
        setForm(cfgToForm(applied.cfg))
        setBaseVersion(next.applied.version)
        return
      }
      if (res.status === 409) {
        setConfirmIssues(res.body.message || 'План змінив інший користувач. Оновіть сторінку.')
        return
      }
      const lines = [res.body.invalid, res.body.message, ...(res.body.issues ?? []).map((x) => timeLabel(clock, x.hour) + '–' + timeLabel(clock, x.hour + 1) + ': ' + x.text)]
      setConfirmIssues(lines.filter(Boolean).join('\n'))
    } catch (e) {
      setConfirmIssues(String(e))
    } finally {
      setConfirmBusy(false)
    }
  }

  // --- chart wiring --------------------------------------------------------

  const chartRef = useRef<HTMLDivElement>(null)
  const dragging = useRef(false)
  const [width, setWidth] = useState(900)
  // A past day replaces the desk view; the draft stays in this component.
  const [pastDay, setPastDay] = useState<string | null>(null)
  const hasState = !!state
  useEffect(() => {
    const el = chartRef.current
    if (!el) return
    const ro = new ResizeObserver((entries) => {
      const w = entries[0].contentRect.width
      setWidth((prev) => (Math.abs(prev - w) > 1 ? w : prev))
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [hasState, pastDay])

  // hitIndex maps a pointer x to a desk hour of the rendered layout
  // (read only from event handlers — the DOM rect is live there).
  const hitIndex = (l: ChartLayout, clientX: number): number => {
    const el = chartRef.current
    if (!el) return 0
    const r = el.getBoundingClientRect()
    return Math.max(l.first, Math.min(l.first + l.count - 1, l.first + Math.floor((clientX - r.left - l.left) / l.step)))
  }

  // startDrag follows the pointer across the window until release; the
  // layout cannot change mid-drag, so the closure's copy is current.
  const startDrag = (l: ChartLayout) => {
    dragging.current = true
    const move = (e: PointerEvent) => {
      const i = Math.max(0, hitIndex(l, e.clientX))
      setSel((s) => {
        const start = Math.min(s.anchor, i)
        const end = Math.max(s.anchor, i) + 1
        return start === s.start && end === s.end && s.inspect === i ? s : { ...s, start, end, inspect: i }
      })
    }
    const up = () => {
      dragging.current = false
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', up)
  }

  if (error && !state) return <div className="ctl-notice err">{error}</div>
  if (!state || !active || !working || !form || !cfg || !clock || !result || !siteInfo || !chartModel)
    return <div className="ctl-placeholder">Завантаження пульта…</div>

  const today = localDate(state.now, state.timezone)
  if (pastDay)
    return (
      <div className="dispatch-desk">
        <DeskDay
          site={site}
          date={pastDay}
          maxDate={today}
          timezone={state.timezone}
          onDate={setPastDay}
          onClose={() => setPastDay(null)}
          onOpenReport={onOpenReport}
        />
      </div>
    )

  const hours = state.hours
  const at = (i: number) => hours[i + HISTORY_HOURS]
  const past = historyPoints(hours)
  const pvAt = (i: number) => (i < 0 ? (at(i)?.fact?.pv_kw ?? null) : (at(i)?.pv_kw ?? null))
  const loadAt = (i: number) => (i < 0 ? (at(i)?.fact?.load_kw ?? null) : (chartModel.loads[i] ?? null))
  const priceAt = (i: number) => at(i)?.rdn_uah_per_kwh ?? null
  const pointAt = (res: SimResult, i: number): ChartPoint =>
    i < 0 ? (past[i + HISTORY_HOURS] ?? EMPTY_POINT) : pointFromResult(res.hours[i])
  const previewAt = (i: number) => pointAt(result, i)
  const baselineAt = (i: number) => pointAt(state.result, i)
  const planAt = (i: number): PlanOverlay | null => planOverlay(i < 0 ? at(i)?.plan : undefined)
  const reserve = invalid ? working.cfg.reserve_pct : cfg.reserve_pct
  const first = -HISTORY_HOURS
  const count = HISTORY_HOURS + FUTURE_HOURS
  const visible = Array.from({ length: count }, (_, k) => first + k)
  const modes: ModeGroup[] = []
  for (const i of visible) {
    const st = hourPlanState(active, working, i)
    const last = modes[modes.length - 1]
    if (last && last.key === st.key) last.end = i + 1
    else modes.push({ key: st.key, label: st.label, short: st.short, start: i, end: i + 1 })
  }
  const chart = buildChartSvg({
    width,
    clock,
    first,
    count,
    series,
    layout,
    values: true,
    reserve,
    start: sel.start,
    end: sel.end,
    inspectHour: sel.inspect,
    editable: true,
    pvAt,
    loadAt,
    priceAt,
    previewAt,
    baselineAt,
    planAt,
    actions: actionGroups(chartModel, active, working, visible),
    modes,
  })

  const span = timeLabel(clock, sel.start) + '–' + timeLabel(clock, sel.end)
  const selected = result.hours.slice(sel.start, sel.end)
  const knownLoads = chartModel.loads.slice(sel.start, sel.end).every(hasValue)
  const mean = (xs: (number | null)[]) => xs.slice(sel.start, sel.end).reduce<number>((v, x) => v + (x ?? 0), 0) / (sel.end - sel.start)
  const futurePv = Array.from({ length: FUTURE_HOURS }, (_, i) => at(i)?.pv_kw ?? 0)
  const prices = Array.from({ length: FUTURE_HOURS }, (_, i) => at(i)?.rdn_uah_per_kwh).slice(sel.start, sel.end).filter(hasValue)
  const issues = result.issues.filter((x) => x.blocking)
  const startSoc = result.hours[sel.start]?.soc_before_pct ?? null
  const endSoc = result.hours[sel.end - 1]?.soc_pct ?? null
  const selectedSoc = selected.map((h) => h.soc_pct)
  const completeSoc = hasValue(startSoc) && selectedSoc.every(hasValue)
  const outcome = commandOutcome(clock, result, chartModel, hours, scenario, paramRaw, direction, sel.start, sel.end)
  const statusLine =
    statusState.key === fieldsKey || invalid || same(working, previewModel)
      ? status
      : 'Попередній перегляд · ці зміни ще не додано у чернетку.'

  const review = reviewGroups(active, working)
  const reviewResult = reviewOpen ? result : null
  const reviewBlockers = reviewResult ? reviewResult.issues.filter((x) => x.blocking) : []
  const reviewReserveError =
    form.reserve.trim() === '' || !(Number(form.reserve) >= siteInfo.soc_min_pct && Number(form.reserve) <= siteInfo.soc_max_pct)
      ? 'Резерв SOC має бути від ' + fmt(siteInfo.soc_min_pct) + '% до ' + fmt(siteInfo.soc_max_pct) + '%.'
      : ''
  const canConfirm = reviewOpen && !stale && !same(active, working) && reviewBlockers.length === 0 && !reviewReserveError && !confirmBusy

  const startSummary =
    'SOC ' + fmt(state.start_soc_pct) + '%' + (state.start_soc_known ? '' : ' (немає свіжої телеметрії — середина вікна)') + ' · ' +
    fmt(siteInfo.capacity_kwh) + ' кВт·год · до ' + fmt(Math.max(siteInfo.charge_kw, siteInfo.discharge_kw)) + ' кВт'

  return (
    <div className="dispatch-desk">
      {notice && <div className="ctl-notice">{notice}</div>}
      {error && <div className="ctl-notice err">{error}</div>}
      <div className="d-heading">
        <div>
          <h2>Ручне керування УЗЕ</h2>
          <p className="d-small">{startSummary}</p>
        </div>
        <div className="d-row">
          <span className="d-small">32 години · 8 назад + 24 уперед</span>
          <PeriodPicker
            preset="day"
            anchor={dateFromISO(today)}
            onChange={(d) => {
              const iso = isoFromDate(d)
              if (iso < today) setPastDay(iso)
            }}
          />
          {onOpenReport && (
            <button type="button" onClick={() => onOpenReport(today)}>
              Звіт за день
            </button>
          )}
          <button type="button" onClick={() => setSel({ start: 0, end: 1, anchor: 0, inspect: 0 })}>
            Зараз на 1 год
          </button>
        </div>
      </div>

      <section className={'d-workspace' + (reviewOpen ? ' d-reviewing' : '')} aria-label="Контекст і швидке керування">
        <div className="d-context">
          <ChartToolbar
            stale={stale}
            layout={layout}
            onLayout={setLayout}
            series={series}
            onToggle={(key) => setSeries((s) => ({ ...s, [key]: !s[key] }))}
          />
          <div
            ref={chartRef}
            className="d-chart"
            role="img"
            aria-label={
              'Керування УЗЕ. ' +
              (completeSoc
                ? 'SOC: ' + fmt(startSoc) + '% на початку вибраного інтервалу, ' + fmt(endSoc) + '% наприкінці.'
                : 'Прогноз SOC для вибраного інтервалу відсутній: споживання або ціна РДН задані не для всіх попередніх годин.') +
              ' Горизонт ' + timeLabel(clock, first) + '–' + timeLabel(clock, first + count) + '. Минуле не редагується.'
            }
            onPointerDown={(e) => {
              if (!(e.target as Element).closest('[data-brush]') || e.button !== 0) return
              const i = hitIndex(chart, e.clientX)
              if (i < 0) {
                setSel((s) => ({ ...s, inspect: i }))
                return
              }
              select(i, e.shiftKey)
              startDrag(chart)
            }}
            onPointerMove={(e) => {
              if (dragging.current || !(e.target as Element).closest('[data-brush]')) return
              const i = hitIndex(chart, e.clientX)
              setSel((s) => (s.inspect === i ? s : { ...s, inspect: i }))
            }}
            dangerouslySetInnerHTML={{ __html: chart.svg }}
          />
          <ChartNotes
            series={series}
            overflow={chart.overflow}
            clock={clock}
            span={span}
            startSoc={startSoc}
            endSoc={endSoc}
            selectedSoc={selectedSoc}
            completeSoc={completeSoc}
            reserve={reserve}
          />
          <Inspector
            clock={clock}
            i={sel.inspect}
            point={previewAt(sel.inspect)}
            pv={pvAt(sel.inspect)}
            load={loadAt(sel.inspect)}
            price={priceAt(sel.inspect)}
            buy={at(sel.inspect)?.buy_uah_per_kwh ?? null}
            sell={at(sel.inspect)?.sell_uah_per_kwh ?? null}
            plan={planAt(sel.inspect)}
            state={hourPlanState(active, working, sel.inspect)}
            series={series}
          />
        </div>

        <aside className="d-command" aria-label="Параметри ручної команди">
          <div className="d-edit">
            <IntervalHead
              clock={clock}
              span={span}
              start={sel.start}
              end={sel.end}
              onStart={(start) => setSel((s) => ({ start, end: Math.max(s.end, start + 1), anchor: start, inspect: start }))}
              onEnd={(end) =>
                setSel((s) => {
                  const start = Math.min(s.start, end - 1)
                  return { start, end, anchor: start, inspect: start }
                })
              }
            />

            <LoadEditor
              value={loadInput}
              uniform={uniformLoad}
              inDraft={selectionLoads.some((v, k) => v !== active.loads[sel.start + k])}
              canClear={!selectionLoads.every((v) => v === null)}
              error={loadError}
              onChange={setLoadInput}
              onSet={() => stageLoad(false)}
              onClear={() => stageLoad(true)}
            />

            <CommandFields
              site={siteInfo}
              exportCapKw={cfg.export_cap_kw}
              scenario={scenario}
              onScenario={setScenario}
              param={paramRaw}
              onParam={(value) => setParams((p) => ({ ...p, [scenario]: value }))}
              reserve={form.reserve}
              onReserve={(value) => setForm({ ...form, reserve: value })}
              direction={direction}
              onDirection={setDirection}
            />

            <CommandHelp scenario={scenario} pvKw={mean(futurePv)} loadKw={knownLoads ? mean(chartModel.loads) : null} prices={prices} />

            <ConstraintsPanel
              site={siteInfo}
              form={form}
              onForm={setForm}
              cfg={cfg}
              defaultReserve={state.defaults.reserve_pct}
              stageDisabled={!!cfgError || same(cfg, working.cfg)}
              onStage={stageConstraints}
            />

            <CommandPreview
              invalid={invalid}
              outcome={outcome}
              issues={issues}
              clock={clock}
              commands={chartModel.commands}
              start={sel.start}
              end={sel.end}
            />

            <div className="d-actions">
              <button type="button" className="d-quiet" disabled={!history.length} onClick={undo} aria-label="Скасувати останню зміну чернетки">
                ↶ Скасувати
              </button>
              <button type="button" className="d-primary" disabled={!!invalid} onClick={stage}>
                У чернетку
              </button>
            </div>
          </div>
          <div className="d-footer">
            <span className="d-small d-status" aria-live="polite">
              {statusLine}
            </span>
            <button type="button" disabled={same(active, working)} onClick={openReview}>
              Переглянути чернетку →
            </button>
          </div>

          {reviewOpen && (
            <ReviewPanel
              clock={clock}
              socMinPct={siteInfo.soc_min_pct}
              socMaxPct={siteInfo.soc_max_pct}
              reserve={form.reserve}
              reserveError={reviewReserveError}
              onReserve={reviewReserve}
              working={working}
              onBlockExport={reviewBlockExport}
              review={review}
              result={reviewResult}
              hours={hours}
              blockers={reviewBlockers}
              confirmIssues={confirmIssues}
              canConfirm={canConfirm}
              confirmBusy={confirmBusy}
              stale={stale}
              onClose={closeReview}
              onEditIssue={editIssue}
              onConfirm={() => void confirm()}
            />
          )}
        </aside>
      </section>

      <DeskFootnotes tariffs={state.tariffs} />
    </div>
  )
}
