// DispatchDesk — the manual-control desk that replaces the three-step
// planner (ems-spec docs/specs/ems_manual_control_mvp.md, mockup
// ems-dispatch-desk-browser-v2.html, guide ems-demo-guide.md). The
// operator enters expected load, sets intents on hour intervals, checks
// the preview, stages a draft and confirms it; the server prices every
// version with the same LP the rolling publisher uses.

import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement } from 'react'
import './dispatch.css'
import { buildChartSvg, type ChartLayout, type ModeGroup, type PlanOverlay, type SeriesToggles } from './chartSvg'
import {
  confirmDraft,
  fetchDeskState,
  previewDraft,
  type CommandType,
  type Constraints,
  type DeskState,
  type Issue,
  type SimResult,
} from './dispatchClient'
import {
  EMPTY_POINT,
  FUTURE_HOURS,
  HISTORY_HOURS,
  actionGroups,
  buildPreview,
  cfgSummary,
  clone,
  commandDesc,
  defaultParams,
  flowMeta,
  fmt,
  hasValue,
  historyPoints,
  hourPlanState,
  hoursBetween,
  intents,
  makeClock,
  normalizeModel,
  optionTime,
  paramLabel,
  pointFromResult,
  priceFmt,
  reviewGroups,
  same,
  scenarioOptions,
  shiftModel,
  splitBessFlow,
  timeLabel,
  unknownForecastText,
  type ChartPoint,
  type Clock,
  type DeskModel,
} from './model'
import { commandError, constraintsError } from './validate'

type Props = { site: string }

type CfgForm = {
  reserve: string
  grid: boolean
  essSale: boolean
  blockExport: boolean
  importCap: string
  exportCap: string
}

const cfgToForm = (c: Constraints): CfgForm => ({
  reserve: String(c.reserve_pct),
  grid: c.grid_charge,
  essSale: c.ess_sale,
  blockExport: c.block_export,
  importCap: String(c.import_cap_kw),
  exportCap: c.export_cap_kw === null ? '' : String(c.export_cap_kw),
})

const formToCfg = (f: CfgForm): Constraints => ({
  reserve_pct: Number(f.reserve),
  grid_charge: f.grid,
  ess_sale: f.essSale,
  block_export: f.blockExport,
  import_cap_kw: Number(f.importCap),
  export_cap_kw: f.exportCap.trim() === '' ? null : Number(f.exportCap),
})

type Selection = { start: number; end: number; anchor: number; inspect: number }

type SavedDraft = { start_hour: string; base_version: number; working: DeskModel; history: DeskModel[] }

const draftKey = (site: string) => 'dispatch-desk:' + site
const STATE_REFRESH_MS = 5 * 60_000
const PREVIEW_DEBOUNCE_MS = 250
const HISTORY_LIMIT = 30

function readDraft(site: string): SavedDraft | null {
  try {
    const raw = window.localStorage.getItem(draftKey(site))
    return raw ? (JSON.parse(raw) as SavedDraft) : null
  } catch {
    return null
  }
}

function writeDraft(site: string, d: SavedDraft | null) {
  try {
    if (d) window.localStorage.setItem(draftKey(site), JSON.stringify(d))
    else window.localStorage.removeItem(draftKey(site))
  } catch {
    /* storage full or blocked — the draft lives in memory only */
  }
}

export function DispatchDesk({ site }: Props) {
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
  const [layout, setLayout] = useState<'split' | 'combined'>('split')
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
  }, [hasState])

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
  const planAt = (i: number): PlanOverlay | null => {
    const p = i < 0 ? at(i)?.plan : undefined
    return p ? { ess: p.ess_kw, soc: p.soc_pct, grid: p.import_kw - p.export_kw } : null
  }
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
  const complete = selected.every((h) => !h.unknown)
  const knownLoads = chartModel.loads.slice(sel.start, sel.end).every(hasValue)
  const mean = (xs: (number | null)[]) => xs.slice(sel.start, sel.end).reduce<number>((v, x) => v + (x ?? 0), 0) / (sel.end - sel.start)
  const futurePv = Array.from({ length: FUTURE_HOURS }, (_, i) => at(i)?.pv_kw ?? 0)
  const prices = Array.from({ length: FUTURE_HOURS }, (_, i) => at(i)?.rdn_uah_per_kwh).slice(sel.start, sel.end).filter(hasValue)
  const charged = selected.reduce((n, x) => n + Math.max(0, -(x.p_kw ?? 0)), 0)
  const discharged = selected.reduce((n, x) => n + Math.max(0, x.p_kw ?? 0), 0)
  const exported = selected.reduce((n, x) => n + (x.export_kw ?? 0), 0)
  const issues = result.issues.filter((x) => x.blocking)
  const commandIssues = issues.filter((x) => x.hour >= sel.start && x.hour < sel.end)
  const laterIssues = issues.filter((x) => x.hour >= sel.end)
  const earlierIssues = issues.filter((x) => x.hour < sel.start)
  const startSoc = result.hours[sel.start]?.soc_before_pct ?? null
  const endSoc = result.hours[sel.end - 1]?.soc_pct ?? null
  const selectedSoc = selected.map((h) => h.soc_pct)
  const completeSoc = hasValue(startSoc) && selectedSoc.every(hasValue)
  const exportCeiling = siteInfo.export_ceiling_kw

  let flowLabel: string
  let flowValue: string
  let flowDetail: string
  let comparison = ''
  if (!complete) {
    flowLabel = 'Результат команди'
    flowValue = 'Немає прогнозу'
    flowDetail = unknownForecastText(clock, result, chartModel, hours)
    if (scenario === 'export' || scenario === 'fixed') comparison = 'Запитано ' + fmt(Number(paramRaw) * (sel.end - sel.start)) + ' кВт·год'
  } else if (scenario === 'cap') {
    const peak = Math.max(...selected.map((x) => x.import_kw ?? 0))
    const limit = Number(paramRaw)
    flowLabel = 'AUTO · пік імпорту'
    flowValue = fmt(peak) + ' кВт'
    flowDetail =
      'Заданий ліміт ' + fmt(limit) + ' кВт' + (peak > limit + 0.05 ? ' · перевищення ' + fmt(peak - limit) + ' кВт' : ' · дотримано') +
      '. AUTO: заряд ' + fmt(charged) + ' · розряд ' + fmt(discharged) + ' кВт·год'
  } else if (scenario === 'target') {
    flowLabel = 'SOC наприкінці команди'
    flowValue = fmt(endSoc) + '%'
    flowDetail = 'Ціль ' + fmt(Number(paramRaw)) + '% · заряд ' + fmt(charged) + ' кВт·год'
  } else {
    flowLabel =
      scenario === 'export' ? 'Експорт PCC за інтервал' : discharged > 0 ? 'Розряд УЗЕ за інтервал' : charged > 0 ? 'Заряд УЗЕ за інтервал' : 'УЗЕ за інтервал'
    flowValue = fmt(scenario === 'export' ? exported : discharged > 0 ? discharged : charged) + ' кВт·год'
    flowDetail =
      scenario === 'export'
        ? 'УЗЕ: заряд ' + fmt(charged) + ' · розряд ' + fmt(discharged) + ' кВт·год'
        : charged > 0 && discharged > 0
          ? 'Заряд УЗЕ за інтервал: ' + fmt(charged) + ' кВт·год'
          : discharged > 0
            ? 'Енергія, віддана УЗЕ'
            : charged > 0
              ? 'Енергія, отримана УЗЕ'
              : 'Заряд і розряд відсутні'
    if (scenario === 'export' || scenario === 'fixed') {
      const requested = Number(paramRaw) * (sel.end - sel.start)
      const actual = scenario === 'export' ? exported : direction === 'charge' ? charged : discharged
      const shortfall = Math.max(0, requested - actual)
      comparison = 'Запитано ' + fmt(requested) + ' кВт·год' + (shortfall > 0.05 ? ' · бракує ' + fmt(shortfall) + ' кВт·год' : ' · виконується повністю')
    }
  }
  const statusLine =
    statusState.key === fieldsKey || invalid || same(working, previewModel)
      ? status
      : 'Попередній перегляд · ці зміни ще не додано у чернетку.'

  const issueRows = (items: Issue[]) =>
    items.map((x) => (
      <li key={x.hour + x.text}>
        {timeLabel(clock, x.hour)}–{timeLabel(clock, x.hour + 1)} · {commandDesc(chartModel.commands[x.hour])}: {x.text}
      </li>
    ))
  const issueGroup = (label: string, items: Issue[]) =>
    items.length ? (
      <>
        <div className="d-warning-group">{label}</div>
        <ul>{issueRows(items.slice(0, 2))}</ul>
        {items.length > 2 && (
          <details>
            <summary>Ще {items.length - 2} інтервалів</summary>
            <ul>{issueRows(items.slice(2))}</ul>
          </details>
        )}
      </>
    ) : null

  const review = reviewGroups(active, working)
  const reviewResult = reviewOpen ? result : null
  const reviewBlockers = reviewResult ? reviewResult.issues.filter((x) => x.blocking) : []
  const reviewReserveError =
    form.reserve.trim() === '' || !(Number(form.reserve) >= siteInfo.soc_min_pct && Number(form.reserve) <= siteInfo.soc_max_pct)
      ? 'Резерв SOC має бути від ' + fmt(siteInfo.soc_min_pct) + '% до ' + fmt(siteInfo.soc_max_pct) + '%.'
      : ''
  const canConfirm = reviewOpen && !stale && !same(active, working) && reviewBlockers.length === 0 && !reviewReserveError && !confirmBusy

  const fromOptions = Array.from({ length: FUTURE_HOURS }, (_, i) => i)
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
          <button type="button" onClick={() => setSel({ start: 0, end: 1, anchor: 0, inspect: 0 })}>
            Зараз на 1 год
          </button>
        </div>
      </div>

      <section className={'d-workspace' + (reviewOpen ? ' d-reviewing' : '')} aria-label="Контекст і швидке керування">
        <div className="d-context">
          <div className="d-chart-head">
            <h3>Прогноз і керування{stale ? ' · розрахунок…' : ''}</h3>
            <div className="d-chart-modes" role="group" aria-label="Компонування графіка">
              {(['split', 'combined'] as const).map((l) => (
                <button key={l} type="button" aria-pressed={layout === l} onClick={() => setLayout(l)}>
                  {l === 'split' ? 'Розділено' : 'Разом'}
                </button>
              ))}
            </div>
          </div>
          <div className="d-series-controls" role="group" aria-label="Показники на графіку">
            {(
              [
                ['price', 'd-price', 'РДН'],
                ['pv', 'd-pv', 'СЕС'],
                ['load', 'd-load', 'Споживання'],
                ['grid', 'd-grid', 'Мережа PCC'],
                ['bess', 'd-bess', 'УЗЕ'],
                ['soc', 'd-soc', 'SOC'],
                ['before', 'd-old', 'До зміни'],
                ['plan', 'd-plan', 'План (історія)'],
              ] as const
            ).map(([key, cls, label]) => (
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
          <div
            ref={chartRef}
            className="d-chart"
            role="img"
            aria-label={
              'Керування УЗЕ. ' +
              (completeSoc
                ? 'SOC: ' + fmt(startSoc) + '% на початку вибраного інтервалу, ' + fmt(endSoc) + '% наприкінці.'
                : 'Прогноз SOC для вибраного інтервалу відсутній.')
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
          {chart.overflow.length > 0 && (
            <div className="d-action-overflow" aria-label="Команди коротких інтервалів">
              {chart.overflow.map((g, k) => (
                <span key={k} className={g.status === 'preview' ? 'is-preview' : ''}>
                  {k + 1}. {timeLabel(clock, g.start)}–{timeLabel(clock, g.end)} · {g.mark.detail}
                  {g.status === 'preview' ? ' · перегляд' : ''}
                </span>
              ))}
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
            {series.soc && (
              <span className="d-soc-readout" aria-live="polite">
                <span>SOC · {span}</span>
                {completeSoc ? (
                  <>
                    <strong>
                      {fmt(startSoc)}% → {fmt(endSoc)}%
                    </strong>
                    <span>
                      мін. {fmt(Math.min(startSoc as number, ...(selectedSoc as number[])))}% · резерв {fmt(reserve)}%
                    </span>
                  </>
                ) : (
                  <>
                    <strong>Немає прогнозу</strong>
                    <span>
                      {hasValue(startSoc) ? 'на початку ' + fmt(startSoc) + '% · ' : ''}резерв {fmt(reserve)}%
                    </span>
                  </>
                )}
              </span>
            )}
            <span>Клік — деталі · протягніть — інтервал</span>
          </div>
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
            <div className="d-edit-head">
              <strong aria-live="polite">
                Інтервал · {span} · {sel.end - sel.start} год
              </strong>
              <div className="d-range">
                <label>
                  Від
                  <select
                    value={sel.start}
                    onChange={(e) => {
                      const start = Number(e.target.value)
                      setSel((s) => ({ start, end: Math.max(s.end, start + 1), anchor: start, inspect: start }))
                    }}
                  >
                    {fromOptions.map((i) => (
                      <option key={i} value={i}>
                        {optionTime(clock, i)}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  До
                  <select
                    value={sel.end}
                    onChange={(e) => {
                      const end = Number(e.target.value)
                      setSel((s) => {
                        const start = Math.min(s.start, end - 1)
                        return { start, end, anchor: start, inspect: start }
                      })
                    }}
                  >
                    {fromOptions.map((i) => (
                      <option key={i + 1} value={i + 1}>
                        {optionTime(clock, i + 1)}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
            </div>

            <section className="d-load-editor" aria-label="Споживання у вибрані години">
              <label className="d-field">
                Очікуване споживання
                <span className="d-input-unit">
                  <input
                    type="number"
                    min={0}
                    step={10}
                    placeholder={uniformLoad ? 'Не задано' : 'Різні значення'}
                    value={loadInput}
                    onChange={(e) => setLoadInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault()
                        stageLoad(false)
                      }
                    }}
                    aria-label="Споживання для вибраних годин"
                  />
                  <span>кВт</span>
                </span>
              </label>
              <div className="d-load-actions">
                <button type="button" onClick={() => stageLoad(false)}>
                  Задати
                </button>
                <button type="button" className="d-quiet" disabled={selectionLoads.every((v) => v === null)} onClick={() => stageLoad(true)}>
                  Очистити
                </button>
              </div>
              <p className="d-small" aria-live="polite">
                {selectionLoads.some((v, k) => v !== active.loads[sel.start + k]) ? 'У чернетці. ' : ''}
                Порожньо = невідомо; 0 = споживання немає.
              </p>
              {loadError && (
                <p className="d-small d-error" role="alert">
                  {loadError}
                </p>
              )}
            </section>

            <div className="d-fields">
              <label className="d-field">
                Що потрібно зробити
                <select value={scenario} onChange={(e) => setScenario(e.target.value as CommandType)}>
                  {scenarioOptions.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              </label>
              {scenario !== 'hold' && scenario !== 'auto' && (
                <label className="d-field">
                  <span>{paramLabel(scenario)}</span>
                  <span className="d-input-unit">
                    <input
                      type="number"
                      min={0}
                      step={scenario === 'target' ? 5 : 10}
                      max={
                        scenario === 'target'
                          ? siteInfo.soc_max_pct
                          : scenario === 'cap'
                            ? siteInfo.import_kw
                            : scenario === 'export'
                              ? (cfg.export_cap_kw ?? undefined)
                              : Math.max(siteInfo.charge_kw, siteInfo.discharge_kw)
                      }
                      value={paramRaw}
                      onChange={(e) => setParams((p) => ({ ...p, [scenario]: e.target.value }))}
                    />
                    <span>{scenario === 'target' ? '%' : 'кВт'}</span>
                  </span>
                </label>
              )}
              <label className="d-field">
                Резерв SOC для чернетки
                <span className="d-input-unit">
                  <input
                    type="number"
                    min={siteInfo.soc_min_pct}
                    max={siteInfo.soc_max_pct}
                    step={5}
                    value={form.reserve}
                    onChange={(e) => setForm({ ...form, reserve: e.target.value })}
                    aria-label="Операційний резерв SOC"
                  />
                  <span>%</span>
                </span>
              </label>
              {scenario === 'fixed' && (
                <label className="d-field">
                  Напрямок потужності
                  <select value={direction} onChange={(e) => setDirection(e.target.value as 'charge' | 'discharge')}>
                    <option value="discharge">Розряд</option>
                    <option value="charge">Заряд</option>
                  </select>
                </label>
              )}
            </div>

            <details className="d-help">
              <summary>Пояснення дії та прогноз інтервалу</summary>
              <p className="d-intent">{intents[scenario]}</p>
              <p className="d-small">
                Після завершення ручної команди AUTO перерахує план від фактичного стану. Інші ручні інтервали залишаються заданими. Раніші ручні
                команди мають пріоритет: пізніша команда не зменшує їх виконання.
              </p>
              <div className="d-small">
                У вибрані години: СЕС у середньому {fmt(mean(futurePv))} кВт · споживання{' '}
                {knownLoads ? fmt(mean(chartModel.loads)) + ' кВт' : 'задано не для всіх годин'} · РДН{' '}
                {prices.length ? fmt(Math.min(...prices)) + '–' + fmt(Math.max(...prices)) : '—'} грн/кВт·год
              </div>
            </details>

            <details>
              <summary>Обмеження на весь горизонт</summary>
              <div className="d-constraints">
                <label className="d-field">
                  Резерв SOC
                  <span className="d-input-unit">
                    <input
                      type="number"
                      min={siteInfo.soc_min_pct}
                      max={siteInfo.soc_max_pct}
                      step={5}
                      value={form.reserve}
                      onChange={(e) => setForm({ ...form, reserve: e.target.value })}
                      aria-label="Резерв SOC на весь горизонт"
                    />
                    <span>%</span>
                  </span>
                </label>
                <div className="d-rule-note">Спільний резерв для всієї чернетки; синхронізований із полем біля команди.</div>
                <label className="d-check">
                  <input type="checkbox" checked={form.grid} onChange={(e) => setForm({ ...form, grid: e.target.checked })} />
                  Дозволити заряд із мережі
                </label>
                <label className="d-check">
                  <input type="checkbox" checked={form.blockExport} onChange={(e) => setForm({ ...form, blockExport: e.target.checked })} />
                  Повністю заборонити експорт · СЕС та УЗЕ
                </label>
                <label className="d-check">
                  <input
                    type="checkbox"
                    checked={form.essSale}
                    disabled={form.blockExport || exportCeiling === null}
                    onChange={(e) => setForm({ ...form, essSale: e.target.checked })}
                  />
                  Продаж енергії УЗЕ
                </label>
                <label className="d-field">
                  Ліміт імпорту, кВт
                  <input
                    type="number"
                    min={0}
                    max={siteInfo.import_kw}
                    step={10}
                    value={form.importCap}
                    onChange={(e) => setForm({ ...form, importCap: e.target.value })}
                  />
                </label>
                <label className="d-field">
                  Ліміт експорту PCC, кВт
                  <input
                    type="number"
                    min={0}
                    max={exportCeiling ?? undefined}
                    step={10}
                    placeholder="Не заданий"
                    disabled={form.blockExport || exportCeiling === null}
                    value={form.exportCap}
                    onChange={(e) => setForm({ ...form, exportCap: e.target.value })}
                  />
                </label>
                <div className="d-rule-note">
                  Паспорт: імпорт до {siteInfo.import_set ? fmt(siteInfo.import_kw) + ' кВт' : 'не задано'}; заряд / розряд УЗЕ до {fmt(siteInfo.charge_kw)} /{' '}
                  {fmt(siteInfo.discharge_kw)} кВт; ємність {fmt(siteInfo.capacity_kwh)} кВт·год; СЕС до {fmt(siteInfo.pv_rated_kw)} кВт AC. Вікно SOC{' '}
                  {fmt(siteInfo.soc_min_pct)}–{fmt(siteInfo.soc_max_pct)}% («Обмеження»).
                </div>
                <div className="d-rule-note">
                  <button type="button" disabled={!!cfgError || same(cfg, working.cfg)} onClick={stageConstraints}>
                    У чернетку лише обмеження
                  </button>
                </div>
                <div className="d-rule-note">
                  {form.blockExport
                    ? 'Експорт у точці приєднання = 0 кВт для СЕС і УЗЕ. Надлишок СЕС заряджає батарею, коли це можливо; решта обмежується. Після зняття заборони повернеться заданий ліміт.'
                    : exportCeiling === null
                      ? 'Режим відпуску в мережу для обʼєкта не обрано («Обмеження»), тому ліміту експорту немає й експорт вимкнений; надлишок СЕС обмежується.'
                      : 'Стеля відпуску PCC за паспортом — ' + fmt(exportCeiling) + ' кВт. Продаж енергії УЗЕ дозволяється окремо.'}
                </div>
              </div>
            </details>

            {!invalid && (
              <div className="d-preview" aria-live="polite">
                <div className="d-metric">
                  <span>{flowLabel}</span>
                  <strong>{flowValue}</strong>
                  {comparison && <span className="d-compare">{comparison}</span>}
                  <span>{flowDetail}</span>
                </div>
              </div>
            )}

            {(invalid || issues.length > 0) && (
              <div className="d-warning" role="alert">
                {invalid ? (
                  invalid
                ) : (
                  <>
                    <strong>{commandIssues.length ? 'Команда не виконується повністю.' : 'Є невиконані умови в інших годинах.'}</strong>
                    {issueGroup('У вибраному інтервалі', commandIssues)}
                    {issueGroup('Інші інтервали після ' + timeLabel(clock, sel.end), laterIssues)}
                    {issueGroup('Інші інтервали до ' + timeLabel(clock, sel.start), earlierIssues)}
                    <p>Можна додати у чернетку й продовжити редагування. Перед застосуванням потрібно виправити позначені інтервали.</p>
                  </>
                )}
              </div>
            )}

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
            <section className="d-review" aria-label="Підтвердження чернетки">
              <div className="d-row" style={{ justifyContent: 'space-between' }}>
                <h3>Перевірте чернетку</h3>
                <button type="button" className="d-quiet" onClick={closeReview} aria-label="Закрити чернетку">
                  ✕
                </button>
              </div>
              <div className="d-review-reserve">
                <label className="d-field">
                  Резерв SOC для всієї чернетки
                  <span className="d-input-unit">
                    <input
                      type="number"
                      min={siteInfo.soc_min_pct}
                      max={siteInfo.soc_max_pct}
                      step={5}
                      value={form.reserve}
                      onChange={(e) => reviewReserve(e.target.value)}
                      aria-label="Резерв SOC у чернетці"
                    />
                    <span>%</span>
                  </span>
                </label>
                <p className="d-small">Зміна перераховує чернетку перед застосуванням.</p>
                {reviewReserveError && (
                  <p className="d-small d-error" role="alert">
                    {reviewReserveError}
                  </p>
                )}
              </div>
              <label className="d-check">
                <input type="checkbox" checked={working.cfg.block_export} onChange={(e) => reviewBlockExport(e.target.checked)} />
                Повністю заборонити експорт · СЕС та УЗЕ
              </label>
              <div className="d-review-list">
                {review.loads.map((g) => (
                  <div className="d-review-item" key={'l' + g.start}>
                    <strong>
                      {timeLabel(clock, g.start)}–{timeLabel(clock, g.end)}
                    </strong>
                    <span>{g.text}</span>
                  </div>
                ))}
                {review.commands.map((g) => (
                  <div className="d-review-item" key={'c' + g.start}>
                    <strong>
                      {timeLabel(clock, g.start)}–{timeLabel(clock, g.end)}
                    </strong>
                    <span>{g.text}</span>
                  </div>
                ))}
                {review.cfgChanged && (
                  <div className="d-review-item">
                    <strong>Для всієї чернетки</strong>
                    <span>{cfgSummary(working.cfg)}</span>
                  </div>
                )}
              </div>
              <div className="d-review-checks">
                {reviewResult && reviewResult.known_hours < FUTURE_HOURS && (
                  <p className="d-small">
                    {unknownForecastText(clock, reviewResult, working, hours)} Команди можна зберегти; досяжність у цих годинах ще не оцінена.
                  </p>
                )}
                {reviewBlockers.length > 0 && (
                  <div className="d-warning">
                    <strong>Перед застосуванням виправте позначені інтервали.</strong>
                    {reviewBlockers.map((x) => (
                      <div className="d-issue-edit" key={x.hour + x.text}>
                        <span>
                          {timeLabel(clock, x.hour)}–{timeLabel(clock, x.hour + 1)}: {x.text}
                        </span>
                        <button type="button" onClick={() => editIssue(x.hour)}>
                          Редагувати {timeLabel(clock, x.hour)}–{timeLabel(clock, x.hour + 1)}
                        </button>
                      </div>
                    ))}
                  </div>
                )}
                {confirmIssues && (
                  <div className="d-warning" style={{ whiteSpace: 'pre-line' }}>
                    {confirmIssues}
                  </div>
                )}
              </div>
              <div className="d-review-foot">
                <span className="d-small">Shadow: план піде на edge погодинно, запису в SmartLogger немає.</span>
                <button type="button" className="d-primary" disabled={!canConfirm} onClick={() => void confirm()}>
                  {confirmBusy ? 'Підтвердження…' : stale ? 'Розрахунок…' : 'Підтвердити'}
                </button>
              </div>
            </section>
          )}
        </aside>
      </section>

      <details className="d-bottom">
        <summary>Як рахується план</summary>
        <p>
          Погодинний розрахунок, сталі значення всередині години. Прогноз УЗЕ, мережі та SOC будується від поточного SOC лише до першої години без
          заданого споживання або без опублікованої ціни РДН; після прогалини запас енергії невідомий. Ручні команди мають пріоритет у порядку часу
          виконання, далі AUTO обирає економічно найкращий план. Залишок енергії понад резерв оцінюється за мінімальною повною ціною імпорту на
          відомому горизонті. Частини стовпчика УЗЕ розраховані з балансу: СЕС спочатку покриває споживання. Підтверджений план перераховується
          кожні 15 хвилин від свіжого SOC; без заданого споживання edge працює на самоспоживання.
        </p>
      </details>
      <details className="d-bottom">
        <summary>Тарифи AUTO</summary>
        <p>
          Купівля: РДН + {priceFmt(state.tariffs.distribution_uah_per_kwh)} розподіл + {priceFmt(state.tariffs.transmission_uah_per_kwh)} передача
          {state.tariffs.supplier_margin_mode === 'pct'
            ? ' + ' + fmt(state.tariffs.supplier_margin_pct) + '% націнки постачальника'
            : ' + ' + priceFmt(state.tariffs.supplier_margin_uah_per_kwh) + ' націнка постачальника'}{' '}
          + {priceFmt(state.tariffs.other_fees_uah_per_kwh)} інші платежі. Продаж: РДН − {fmt(state.tariffs.export_discount * 100)}%. Знос:{' '}
          {priceFmt(state.tariffs.degradation_uah_per_kwh)} грн на кВт·год розряду. {state.tariffs.include_vat ? 'З ПДВ.' : 'Без ПДВ.'} ККД
          повного циклу: {fmt((state.tariffs.roundtrip_efficiency || 0.9) * 100)}%. Тарифи — з налаштувань економіки обʼєкта.
        </p>
      </details>
    </div>
  )
}

type InspectorProps = {
  clock: Clock
  i: number
  point: ChartPoint
  pv: number | null
  load: number | null
  price: number | null
  buy: number | null
  sell: number | null
  plan: PlanOverlay | null
  state: { label: string; detail: string }
  series: SeriesToggles
}

// Inspector — exact values of the hovered / clicked hour (renderInspector).
function Inspector({ clock, i, point: d, pv, load, price, buy, sell, plan, state, series }: InspectorProps): ReactElement {
  const knownPower = hasValue(d.p)
  const knownGrid = hasValue(d.import) && hasValue(d.export)
  const knownSoc = hasValue(d.before) && hasValue(d.soc)
  const flows = knownPower && hasValue(load) && hasValue(pv) ? splitBessFlow(d.p as number, pv, load) : []
  const gridCharge = flows.find((f) => f.key === 'grid')
  const gridChargeKw = gridCharge ? Math.abs(gridCharge.power) : 0
  return (
    <div className="d-inspect" aria-label="Точні значення вибраної години">
      <strong className="d-inspect-time">
        {timeLabel(clock, i)}–{timeLabel(clock, i + 1)} · {state.label}
      </strong>
      {i >= 0 && <span className="d-flow-detail">{state.detail}</span>}
      {series.pv && (
        <span>
          {i < 0 ? 'СЕС' : 'СЕС (прогноз)'} <strong>{fmt(pv)} кВт</strong>
        </span>
      )}
      {series.load && (
        <span>
          Споживання <strong>{hasValue(load) ? fmt(load) + ' кВт' : 'Не задано'}</strong>
        </span>
      )}
      {series.price && (
        <span>
          РДН <strong>{hasValue(price) ? fmt(price) + ' грн/кВт·год' : 'не опублікована'}</strong>
        </span>
      )}
      {series.bess && (
        <span>
          УЗЕ{knownPower ? ' · ' + ((d.p as number) < 0 ? 'заряд' : (d.p as number) > 0 ? 'розряд' : 'утримання') : ''}{' '}
          <strong>{knownPower ? fmt(Math.abs(d.p as number)) + ' кВт' : 'Немає прогнозу'}</strong>
        </span>
      )}
      <span>
        {knownGrid ? ((d.export as number) > 0 ? 'Експорт PCC' : 'Імпорт PCC') : 'Мережа PCC'}{' '}
        <strong>{knownGrid ? fmt((d.import as number) || (d.export as number)) + ' кВт' : 'Немає прогнозу'}</strong>
      </span>
      {series.soc && (
        <span>
          SOC <strong>{knownSoc ? fmt(d.before) + ' → ' + fmt(d.soc) + '%' : hasValue(d.soc) ? fmt(d.soc) + '%' : 'Немає прогнозу'}</strong>
        </span>
      )}
      {series.bess && knownPower && hasValue(load) && (
        <span className="d-flow-detail">
          {flows.length
            ? ((d.p as number) < 0 ? 'Заряд УЗЕ' : 'Розряд УЗЕ') +
              ' · ' +
              flows.map((part) => flowMeta[part.key].detail + ' ' + fmt(Math.abs(part.power)) + ' кВт').join(' · ')
            : 'УЗЕ · утримання, 0 кВт'}
        </span>
      )}
      {knownGrid && (d.import as number) > 0.05 && (
        <span className="d-flow-detail">
          Імпорт із мережі · на споживання <strong>{fmt(Math.max(0, (d.import as number) - gridChargeKw))} кВт</strong> · на заряд УЗЕ{' '}
          <strong>{fmt(gridChargeKw)} кВт</strong>
        </span>
      )}
      {hasValue(d.curtailed) && d.curtailed > 0.05 && (
        <span className="d-flow-detail">
          Обмеження СЕС <strong>{fmt(d.curtailed)} кВт</strong> · надлишок не приймають батарея або мережа
        </span>
      )}
      {plan && (
        <span className="d-flow-detail">
          План на цю годину · УЗЕ <strong>{fmt(plan.ess)} кВт</strong> · SOC <strong>{fmt(plan.soc)}%</strong> · мережа{' '}
          <strong>{fmt(plan.grid)} кВт</strong>
        </span>
      )}
      {series.price && i >= 0 && hasValue(buy) && hasValue(sell) && (
        <span className="d-flow-detail">
          Тарифи AUTO · купівля <strong>{priceFmt(buy)}</strong> · продаж <strong>{priceFmt(sell)}</strong> грн/кВт·год
        </span>
      )}
    </div>
  )
}
