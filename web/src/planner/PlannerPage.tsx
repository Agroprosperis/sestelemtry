import { useCallback, useEffect, useRef, useState } from 'react'
import { LoadEditor } from './LoadEditor'
import { ManifestJournal } from './ManifestJournal'
import { ContextChart, EffectWaterfall, PlanChart, WeatherStrip } from './PlanCharts'
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

// PlannerPage — «План на добу» за мокапом cloud_console.html: кроковий
// візард (споживання → вхідні дані AI → план УЗЕ), горизонт «від зараз
// до кінця відомих РДН», ефект — подобово на завтра.
export function PlannerPage() {
  const [sites, setSites] = useState<string[]>([])
  const [site, setSite] = useState<string>(readSiteFromUrl())
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

  useEffect(() => {
    let cancelled = false
    fetchEdgeSites()
      .then((list) => {
        if (cancelled) return
        setSites(list)
        if (list.length > 0 && !list.includes(site)) {
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
    <div className="planner-page">
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
            <div className="planner-input-kpi"><div className="k">Прогноз PV</div><div className="v">{fmtMwh(tomorrowPvKwh)}</div><div className="sub">МВт·год · завтра</div></div>
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
              {latestManifest && (
                <span className={'planner-chip ' + latestManifest.status}>
                  На edge: {latestManifest.status === 'applied' ? 'застосовано' : latestManifest.status === 'rejected' ? 'відхилено' : 'очікує'}
                </span>
              )}
            </span>
          </div>

          <ContextChart preview={preview} />
          <div className="planner-card" style={{ paddingTop: 10, paddingBottom: 8 }}>
            <WeatherStrip preview={preview} />
          </div>
          <PlanChart preview={preview} onHourClick={setDetailHour} />
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
          {tomorrow && <EffectWaterfall day={tomorrow} dateLabel={tomorrowLabel} />}
          {journal && <ManifestJournal journal={journal} />}
        </>
      )}
    </div>
  )
}
