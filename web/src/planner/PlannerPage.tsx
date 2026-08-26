import { useCallback, useEffect, useRef, useState } from 'react'
import { LoadEditor } from './LoadEditor'
import { ManifestJournal } from './ManifestJournal'
import { ContextChart } from './PlanCharts'
import { EffectPanel, PlanChartSvg } from './PlanStep3'
import {
  clearLoadPlan,
  fetchEdgeSites,
  fetchLoadPlan,
  fetchManifestJournal,
  fetchPlanPreview,
  fetchYesterdayLoadByHour,
  publishManifest,
  saveLoadPlan,
  type LoadPlanEntry,
  type ManifestJournal as Journal,
  type PlanPreview,
  type PlanPreviewHour,
} from './plannerClient'
import './planner.css'

function goBackToDashboard() {
  const url = new URL(window.location.href)
  url.searchParams.delete('view')
  window.history.pushState({}, '', url.toString())
  window.dispatchEvent(new PopStateEvent('popstate'))
}

function readSiteFromUrl(): string {
  const params = new URLSearchParams(window.location.search)
  return (params.get('site') ?? '').trim()
}

function writeSiteToUrl(site: string) {
  const url = new URL(window.location.href)
  url.searchParams.set('site', site)
  window.history.replaceState({}, '', url.toString())
}

function draftToEntries(draft: Map<string, number>): LoadPlanEntry[] {
  return Array.from(draft.entries()).map(([ts, load_kw]) => ({ ts, load_kw }))
}

const LOAD_SOURCE_LABEL: Record<string, string> = {
  operator: 'операторський план',
  operator_partial: 'операторський (частково)',
  heuristic_median_14d: 'heuristic: медіана 14 діб',
  none: 'немає даних load',
}

const fmtUah = (v: number) => Math.round(v).toLocaleString('uk-UA') + ' грн'
const fmtMwh = (kwh: number) => (kwh / 1000).toFixed(1)

type Step = 1 | 2 | 3

// PlanTimeline — «Горизонт від зараз» process track (mockup .plan-timeline).
function PlanTimeline({ preview }: { preview: PlanPreview }) {
  const nowLabel = new Intl.DateTimeFormat('uk-UA', {
    timeZone: preview.timezone,
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date())
  const tomorrowPriced = preview.hours.some((h) => h.tomorrow && h.tradable)
  const steps = [
    {
      state: tomorrowPriced ? 'done' : '',
      t: '~15:30',
      d: tomorrowPriced
        ? 'РДН на завтра опубліковані → горизонт сягає кінця завтра'
        : 'Чекаємо публікацію РДН на завтра (~15:30) — поки що план короткий',
    },
    { state: 'now', t: `зараз ${nowLabel}`, d: `Редагуєте споживання наперед. Старт оптимізації — поточний SOC ${preview.params.start_soc_pct.toFixed(0)}%` },
    { state: '', t: 'ефект', d: 'Очікуваний ефект рахуємо подобово — за завтра; решта сьогодні → у P&L сьогодні' },
    { state: '', t: 'одразу', d: 'EMS перераховує весь горизонт і авто-публікує manifest на edge — без дедлайну' },
    { state: '', t: 'кожні 15 хв', d: 'Rolling: авто-перерахунок від фактичного SOC і перепублікація' },
  ]
  return (
    <div className="planner-timeline">
      <div className="ptl-title">
        Горизонт від «зараз» <span>— до кінця завтра (скільки відомі РДН); минуле — в аналітиці, ефект рахуємо подобово</span>
      </div>
      <div className="ptl-track">
        {steps.map((s) => (
          <div key={s.t} className={'ptl-step ' + s.state}>
            <span className="ptl-dot" />
            <b>{s.t}</b>
            <span className="ptl-desc">{s.d}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

// PlannerPage — «План на добу» за мокапом cloud_console.html: кроковий
// візард (споживання → вхідні дані AI → план УЗЕ), горизонт «від зараз
// до кінця відомих РДН», ефект — подобово на завтра.
//
// embedded=true вбудовує візард як вкладку «План УЗЕ» режиму
// «Керування»: без власної шапки й site-пікера, сайт диктує
// siteOverride (обʼєкт консолі).
export function PlannerPage({
  embedded = false,
  siteOverride,
}: {
  embedded?: boolean
  siteOverride?: string
} = {}) {
  const [sites, setSites] = useState<string[]>([])
  const [site, setSite] = useState<string>(siteOverride ?? readSiteFromUrl())
  const [step, setStep] = useState<Step>(1)
  const [preview, setPreview] = useState<PlanPreview | null>(null)
  const [journal, setJournal] = useState<Journal | null>(null)
  const [draft, setDraft] = useState<Map<string, number>>(new Map())
  const [dirty, setDirty] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [detailHour, setDetailHour] = useState<PlanPreviewHour | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const previewTimer = useRef<number | null>(null)

  // Embedded mode: the console's object picker owns the site.
  useEffect(() => {
    if (siteOverride && siteOverride !== site) setSite(siteOverride)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [siteOverride])

  useEffect(() => {
    let cancelled = false
    fetchEdgeSites()
      .then((list) => {
        if (cancelled) return
        setSites(list)
        if (!siteOverride && list.length > 0 && !list.includes(site)) {
          setSite(list[0])
          writeSiteToUrl(list[0])
        }
      })
      .catch((e) => !cancelled && setError(String(e)))
      .finally(() => !cancelled && setLoading(false))
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const refreshPreview = useCallback(async (siteID: string, draftMap: Map<string, number>) => {
    setPreviewLoading(true)
    try {
      const p = await fetchPlanPreview(siteID, draftToEntries(draftMap))
      setPreview(p)
    } finally {
      setPreviewLoading(false)
    }
  }, [])

  const refreshJournal = useCallback(async (siteID: string) => {
    setJournal(await fetchManifestJournal(siteID))
  }, [])

  useEffect(() => {
    if (!site) return
    let cancelled = false
    setError('')
    setNotice('')
    setPreview(null)
    setDetailHour(null)
    setPreviewLoading(true)
    ;(async () => {
      try {
        // All three are independent: the preview without a draft uses
        // the stored operator plan server-side, so it can run in
        // parallel with fetching that plan for the editor.
        const [stored, p, j] = await Promise.all([
          fetchLoadPlan(site),
          fetchPlanPreview(site, []),
          fetchManifestJournal(site),
        ])
        if (cancelled) return
        setDraft(new Map(stored.map((e) => [e.ts, e.load_kw])))
        setDirty(false)
        setPreview(p)
        setJournal(j)
      } catch (e) {
        if (!cancelled) setError(String(e))
      } finally {
        if (!cancelled) setPreviewLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [site])

  useEffect(() => {
    if (!site) return
    const id = window.setInterval(() => {
      refreshJournal(site).catch(() => {})
    }, 30_000)
    return () => window.clearInterval(id)
  }, [site, refreshJournal])

  const schedulePreview = useCallback(
    (siteID: string, draftMap: Map<string, number>) => {
      setPreviewLoading(true) // видно одразу, ще до дебаунса
      if (previewTimer.current) window.clearTimeout(previewTimer.current)
      previewTimer.current = window.setTimeout(() => {
        refreshPreview(siteID, draftMap).catch((e) => {
          setError(String(e))
          setPreviewLoading(false)
        })
      }, 500)
    },
    [refreshPreview],
  )

  const editHour = (ts: string, kw: number) => {
    const next = new Map(draft)
    next.set(ts, kw)
    setDraft(next)
    setDirty(true)
    setNotice('')
    schedulePreview(site, next)
  }

  const fillUniform = (kw: number) => {
    if (!preview || !Number.isFinite(kw) || kw < 0) return
    const next = new Map(draft)
    for (const h of preview.hours) next.set(h.ts, Math.round(kw * 10) / 10)
    setDraft(next)
    setDirty(true)
    schedulePreview(site, next)
  }

  const fillYesterday = async () => {
    if (!preview) return
    setBusy(true)
    setError('')
    try {
      const byHour = await fetchYesterdayLoadByHour(site, preview.timezone)
      if (byHour.size === 0) {
        setNotice('За вчора немає виміряного load — стовпці не змінено.')
        return
      }
      const next = new Map(draft)
      for (const h of preview.hours) {
        const v = byHour.get(h.local_hour)
        if (v !== undefined) next.set(h.ts, v)
      }
      setDraft(next)
      setDirty(true)
      schedulePreview(site, next)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  const clearAll = async () => {
    setBusy(true)
    setError('')
    try {
      await clearLoadPlan(site)
      const empty = new Map<string, number>()
      setDraft(empty)
      setDirty(false)
      await refreshPreview(site, empty)
      setNotice('Операторський план очищено — діє heuristic-профіль.')
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  const savePlan = async () => {
    if (!dirty) return
    await saveLoadPlan(site, draftToEntries(draft))
    setDirty(false)
  }

  const recalc = async () => {
    setBusy(true)
    setError('')
    try {
      await savePlan()
      await refreshPreview(site, draft)
      setNotice('План перераховано від поточного SOC.')
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  const publish = async () => {
    setBusy(true)
    setError('')
    try {
      await savePlan()
      const res = await publishManifest(site)
      setNotice(
        res.published
          ? `Опубліковано ${res.manifest_id} (${res.intervals} інтервалів).`
          : `План не змінився — чинною лишається версія ${res.manifest_id}.`,
      )
      await refreshJournal(site)
      await refreshPreview(site, draft)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  const tomorrow = preview?.days.find((d) => d.tomorrow)
  const tomorrowLabel = tomorrow
    ? new Intl.DateTimeFormat('uk-UA', { day: '2-digit', month: '2-digit' }).format(
        new Date(`${tomorrow.date}T00:00:00`),
      )
    : ''
  const tomorrowHours = preview?.hours.filter((h) => h.tomorrow) ?? []
  const tomorrowLoadKwh = tomorrowHours.reduce((s, h) => s + (draft.get(h.ts) ?? h.load_kw), 0)
  const tomorrowPvKwh = tomorrowHours.reduce((s, h) => s + h.pv_kw, 0)
  const pricedHours = preview?.hours.filter((h) => h.tradable).length ?? 0
  const latestManifest = journal?.manifests?.[0]

  // Context-card stats (mockup plan-forecast-legend): load peak hour
  // and the RDN min–max over the horizon.
  let loadPeak: { kw: number; label: string } | null = null
  let rdnRange: { min: number; max: number } | null = null
  if (preview) {
    for (const h of preview.hours) {
      const kw = draft.get(h.ts) ?? h.load_kw
      if (!loadPeak || kw > loadPeak.kw) {
        const hh = new Intl.DateTimeFormat('uk-UA', {
          timeZone: preview.timezone,
          hour: '2-digit',
          hour12: false,
        }).format(new Date(h.ts))
        loadPeak = { kw, label: `${h.tomorrow ? 'завтра ' : ''}${hh}:00` }
      }
      if (h.rdn_uah_per_kwh != null) {
        if (!rdnRange) rdnRange = { min: h.rdn_uah_per_kwh, max: h.rdn_uah_per_kwh }
        else {
          rdnRange.min = Math.min(rdnRange.min, h.rdn_uah_per_kwh)
          rdnRange.max = Math.max(rdnRange.max, h.rdn_uah_per_kwh)
        }
      }
    }
  }

  const goToStep = async (next: Step) => {
    if (next === step) return
    if (next >= 2 && dirty) {
      // Крок 2/3 працює зі збереженим планом — тихо зберігаємо чернетку.
      try {
        await savePlan()
      } catch (e) {
        setError(String(e))
        return
      }
    }
    setStep(next)
  }

  return (
    <div className={'planner-page' + (embedded ? ' planner-embedded' : '')}>
      {!embedded && (
        <header className="planner-header-row planner-header">
          <div>
            <h1>План на добу</h1>
            <p>Плануємо наперед: від поточної години до кінця відомих цін РДН. Ефект — подобово, на завтра.</p>
          </div>
          <div className="planner-header-controls">
            {sites.length > 1 && (
              <select
                className="planner-site-select"
                value={site}
                onChange={(e) => {
                  setSite(e.target.value)
                  writeSiteToUrl(e.target.value)
                }}
                aria-label="Обʼєкт"
              >
                {sites.map((s) => (
                  <option key={s} value={s}>
                    {s.toUpperCase()}
                  </option>
                ))}
              </select>
            )}
            <button type="button" className="planner-back-link" onClick={goBackToDashboard}>
              ← Дашборд
            </button>
          </div>
        </header>
      )}

      {preview && (
        <div className="planner-steps">
          <button type="button" className={'planner-step' + (step === 1 ? ' active' : '')} onClick={() => goToStep(1)}>
            <span className="num">1</span>Планове споживання
          </button>
          <button type="button" className={'planner-step' + (step === 2 ? ' active' : '')} onClick={() => goToStep(2)}>
            <span className="num">2</span>Розрахунок AI
          </button>
          <button type="button" className={'planner-step' + (step === 3 ? ' active' : '')} onClick={() => goToStep(3)}>
            <span className="num">3</span>План УЗЕ · перегляд
          </button>
        </div>
      )}

      {preview && <PlanTimeline preview={preview} />}

      {preview && (
        <div className="planner-context-split">
          <aside className="planner-context-aside">
            {dirty ? (
              <div className="planner-ctx-card warn">
                <div className="ctx-head">
                  <span className="ctx-big">Зміни ще не на edge</span>
                  <span className="planner-chip pending">Перепублікувати</span>
                </div>
                Ви змінили споживання → план перераховано від поточного SOC{' '}
                {preview.params.start_soc_pct.toFixed(0)}%. Натисніть «Опублікувати на edge» (крок 3) — або
                дочекайтесь rolling (кожні 15 хв, план збережеться автоматично при переході на крок 2/3).
              </div>
            ) : (
              <div className={'planner-ctx-card ' + (latestManifest?.status === 'applied' ? 'ok' : '')}>
                <div className="ctx-head">
                  <span className="ctx-big">
                    {latestManifest
                      ? latestManifest.status === 'applied'
                        ? 'На edge · активний'
                        : latestManifest.status === 'rejected'
                          ? 'Edge відхилив manifest'
                          : 'Опубліковано · очікує edge'
                      : 'Ще не публікувалось'}
                  </span>
                  {latestManifest && (
                    <span className={'planner-chip ' + latestManifest.status}>
                      {latestManifest.status === 'applied' ? '✓ активний' : latestManifest.status === 'rejected' ? 'відхилено' : 'очікує'}
                    </span>
                  )}
                </div>
                Єдиний план від <b>зараз</b> до кінця <b>завтра</b>. Публікується автоматично після появи РДН
                і rolling кожні 15 хв — окремого дедлайну на публікацію немає.
              </div>
            )}
            <div className="planner-ctx-card">
              <div className="ctx-title">Один план, що котиться вперед</div>
              Плануємо наперед — від «зараз» до кінця завтра (доки відомі РДН). SOC тече через опівніч без
              розриву. Фінанси ріжемо подобово: очікуваний ефект — за завтра; решта сьогодні йде в P&L
              сьогоднішньої доби, а залишок SOC переноситься на наступну добу. Зміна споживання ввечері
              автоматично враховується в ранку наступного дня.
            </div>
          </aside>
          <ContextChart preview={preview}>
            <div className="planner-context-stats">
              <span className="planner-forecast-legend" style={{ marginRight: 14 }}>
                <span><i className="rdn" /> Ціна РДН</span>
                <span><i className="pv" /> Прогноз СЕС</span>
                <span><i className="load" /> План load</span>
              </span>
              Load <b>{fmtMwh(tomorrowLoadKwh)} МВт·год</b> (завтра)
              {loadPeak && (
                <>
                  {' '}· пік <b>{Math.round(loadPeak.kw)} кВт</b> о {loadPeak.label}
                </>
              )}
              {' '}· СЕС <b>{fmtMwh(tomorrowPvKwh)} МВт·год</b>
              {rdnRange && (
                <>
                  {' '}· РДН <b>{rdnRange.min.toFixed(2)}–{rdnRange.max.toFixed(2)} ₴</b>
                </>
              )}
            </div>
          </ContextChart>
        </div>
      )}

      {error && <div className="planner-error">{error}</div>}
      {notice && !error && <div className="planner-card planner-card-sub">{notice}</div>}
      {loading && (
        <div className="planner-loading">
          <span className="planner-spinner" /> Підключаюсь до сервера…
        </div>
      )}

      {!loading && !preview && site && !error && (
        <div className="planner-loading planner-card">
          <span className="planner-spinner" />
          <div>
            <b>Розраховую план…</b>
            <div className="planner-card-sub">
              Збираю ціни РДН, прогноз СЕС, профіль споживання і поточний SOC, потім прогін DP.
              Одразу після рестарту сервера перший розрахунок може тривати до кількох хвилин
              (прогрів профілю споживання) — далі буде миттєво.
            </div>
          </div>
        </div>
      )}

      {preview && previewLoading && (
        <div className="planner-refresh-chip">
          <span className="planner-spinner small" /> перераховую план…
        </div>
      )}

      {!loading && sites.length === 0 && !error && (
        <div className="planner-card">
          <h2>Edge-обʼєкти не налаштовані</h2>
          <p className="planner-card-sub">
            Планувальник працює для обʼєктів з увімкненим edge-контуром (env EDGE_SITE_TOKENS на сервері).
          </p>
        </div>
      )}

      {preview && step === 1 && (
        <>
          <div className="planner-info-box">
            <strong>Горизонт — від «зараз» до кінця відомих РДН.</strong> Батарея не обнуляється опівночі:
            зміна вечора автоматично враховується в ранку наступного дня. Минуле не показуємо — воно у
            фактичній аналітиці. <strong>Очікуваний ефект рахуємо подобово — на завтра.</strong>
          </div>

          <div className="planner-modes">
            <span className="planner-mode active"><b>Авто (макс. профіт)</b>economic_arbitrage · DP-план</span>
            <span className="planner-mode" title="Перемикається фізично на обʼєкті; EMS читає DI (MVP-4)"><b>Острів</b>PV→load→УЗЕ</span>
            <span className="planner-mode" title="Локальний override — на консолі пристрою"><b>Ручне</b>edge console</span>
            <span className="planner-mode" title="MVP-4"><b>Генератор</b>пізніше</span>
          </div>

          <LoadEditor
            hours={preview.hours}
            timezone={preview.timezone}
            draft={draft}
            onEdit={editHour}
            onFillYesterday={fillYesterday}
            onFillUniform={fillUniform}
            onClear={clearAll}
            busy={busy}
          />

          <div className="planner-kpis">
            <span>
              Завтра {tomorrowLabel}: load <b>{fmtMwh(tomorrowLoadKwh)} МВт·год</b>
            </span>
            <span>
              СЕС (прогноз) <b>{fmtMwh(tomorrowPvKwh)} МВт·год</b>
            </span>
            <span>
              Load: <span className={'planner-chip ' + (preview.load_source.startsWith('operator') ? 'operator' : 'heuristic')}>
                {LOAD_SOURCE_LABEL[preview.load_source] ?? preview.load_source}
              </span>
            </span>
            <span style={{ marginLeft: 'auto' }}>
              <button type="button" className="planner-button planner-button-primary" onClick={() => goToStep(2)} disabled={busy}>
                Далі: розрахувати план →
              </button>
            </span>
          </div>
        </>
      )}

      {preview && step === 2 && (
        <div className="planner-card">
          <h2>Вхідні дані для розрахунку</h2>
          <div className="planner-inputs-strip">
            <div className="planner-input-kpi"><div className="k">План load</div><div className="v">{fmtMwh(tomorrowLoadKwh)}</div><div className="sub">МВт·год · завтра</div></div>
            <div className="planner-input-kpi"><div className="k">Прогноз PV</div><div className="v">{fmtMwh(tomorrowPvKwh)}</div><div className="sub">{preview.pv_source === 'generation_forecast' ? 'денний прогноз генерації' : 'оцінка з GTI'} · завтра</div></div>
            <div className="planner-input-kpi"><div className="k">РДН</div><div className="v">{pricedHours > 0 ? '✓' : '—'}</div><div className="sub">{pricedHours} з {preview.hours.length} годин</div></div>
            <div className="planner-input-kpi"><div className="k">SOC зараз</div><div className="v">{preview.params.start_soc_pct.toFixed(0)}%</div><div className="sub">факт (замір), старт плану</div></div>
            <div className="planner-input-kpi"><div className="k">Режим</div><div className="v" style={{ fontSize: 14 }}>Авто</div><div className="sub">макс. профіт</div></div>
            <div className="planner-input-kpi"><div className="k">Ліміт УЗЕ</div><div className="v">{preview.params.power_kw.toFixed(0)}</div><div className="sub">кВт · заряд/розряд</div></div>
            <div className="planner-input-kpi"><div className="k">Резерв SOC</div><div className="v">{preview.params.soc_min_pct}%</div><div className="sub">не в арбітражі</div></div>
          </div>
          <p className="planner-card-sub" style={{ margin: 0 }}>
            Алгоритм: forward DP · all-in ціни · деградація {preview.params.degradation_uah_per_kwh.toFixed(2)} ₴/кВт·год.
            План враховує розряд ≤ {preview.params.power_kw.toFixed(0)} кВт і SOC ≥ {preview.params.soc_min_pct}%.
            {pricedHours === 0 && ' Без цін РДН план не будується — edge працює за preset-правилами.'}
          </p>
          {tomorrow && (
            <p style={{ margin: 0, fontSize: 13, fontWeight: 600, color: tomorrow.net_effect_uah >= 0 ? '#15803d' : '#b91c1c' }}>
              Попередній ефект за завтра: {tomorrow.net_effect_uah >= 0 ? '+' : ''}{fmtUah(tomorrow.net_effect_uah)}
            </p>
          )}
          <div className="planner-kpis">
            <button type="button" className="planner-button" onClick={() => goToStep(1)}>← Назад до споживання</button>
            <button
              type="button"
              className="planner-button planner-button-primary"
              disabled={busy}
              onClick={async () => {
                await recalc()
                setStep(3)
              }}
            >
              ↻ Розрахувати AI-план →
            </button>
          </div>
        </div>
      )}

      {preview && step === 3 && (
        <>
          <div className="planner-kpis">
            <button type="button" className="planner-button" onClick={() => goToStep(1)}>← Споживання</button>
            <button type="button" className="planner-button" onClick={recalc} disabled={busy}>↻ Перерахувати</button>
            <button type="button" className="planner-button planner-button-primary" onClick={publish} disabled={busy}>
              Опублікувати на edge
            </button>
            <span className="planner-badges" style={{ marginLeft: 'auto' }}>
              {tomorrow && (
                <span className="planner-chip operator">
                  Ефект завтра: {tomorrow.net_effect_uah >= 0 ? '+' : ''}{fmtUah(tomorrow.net_effect_uah)}
                </span>
              )}
              <span className="planner-chip heuristic">AI only</span>
              {latestManifest && (
                <span className={'planner-chip ' + latestManifest.status}>
                  На edge: {latestManifest.status === 'applied' ? 'застосовано' : latestManifest.status === 'rejected' ? 'відхилено' : 'очікує'}
                </span>
              )}
            </span>
          </div>

          <div className="planner-card">
            <h2>План заряд/розряд УЗЕ та SOC</h2>
            <p className="planner-card-sub" style={{ margin: 0 }}>
              План вперед: погодинний розклад заряд/розряд УЗЕ, фон РДН, траєкторія SOC та порівняння{' '}
              <strong>без оптимізації vs з AI-планом</strong>. <strong>Горизонт — від «зараз» до кінця завтра</strong>{' '}
              (SOC тече через опівніч; минуле — в аналітиці). Очікуваний ефект — подобово, за завтра.
            </p>
            <div className="uze-layout">
              {tomorrow && <EffectPanel day={tomorrow} dateLabel={tomorrowLabel} preview={preview} />}
              <PlanChartSvg preview={preview} onHourClick={setDetailHour} />
            </div>
          </div>
          {detailHour && (
            <div className="planner-hour-detail">
              <span><b>{new Date(detailHour.ts).toLocaleString('uk-UA', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })}</b>{detailHour.tomorrow ? ' · завтра' : ' · сьогодні'}</span>
              <span>Load: <b>{detailHour.load_kw} кВт</b></span>
              <span>СЕС: <b>{detailHour.pv_kw} кВт</b></span>
              <span>РДН: <b>{detailHour.rdn_uah_per_kwh != null ? detailHour.rdn_uah_per_kwh.toFixed(2) + ' ₴' : '—'}</b> (all-in {detailHour.import_uah_per_kwh.toFixed(2)} ₴)</span>
              <span>УЗЕ: <b>{detailHour.ess_kw > 0 ? '+' : ''}{detailHour.ess_kw} кВт</b> ({detailHour.action})</span>
              <span>SOC на кінець: <b>{detailHour.soc_end_pct}%</b></span>
              <span>Мережа: <b>{detailHour.grid_kw} кВт</b></span>
              <span style={{ color: '#94a3b8' }}>Ручне коригування години — етап керованого запису (MVP-3)</span>
            </div>
          )}
          {journal && <ManifestJournal journal={journal} />}
        </>
      )}
    </div>
  )
}
