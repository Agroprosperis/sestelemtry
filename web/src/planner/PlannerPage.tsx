import { useCallback, useEffect, useRef, useState } from 'react'
import { LoadEditor } from './LoadEditor'
import { ManifestJournal } from './ManifestJournal'
import { ContextChart, EffectWaterfall, PlanChart } from './PlanCharts'
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

// PlannerPage — «План на добу» (cloud console): the operator enters the
// hourly consumption plan, the forward DP recomputes the dispatch
// preview live, and «Опублікувати на edge» ships it as a manifest.
export function PlannerPage() {
  const [sites, setSites] = useState<string[]>([])
  const [site, setSite] = useState<string>(readSiteFromUrl())
  const [preview, setPreview] = useState<PlanPreview | null>(null)
  const [journal, setJournal] = useState<Journal | null>(null)
  const [draft, setDraft] = useState<Map<string, number>>(new Map())
  const [dirty, setDirty] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const previewTimer = useRef<number | null>(null)

  // Site list once; pick the URL site or the first one.
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

  const refreshPreview = useCallback(
    async (siteID: string, draftMap: Map<string, number>) => {
      const p = await fetchPlanPreview(siteID, draftToEntries(draftMap))
      setPreview(p)
    },
    [],
  )

  const refreshJournal = useCallback(async (siteID: string) => {
    setJournal(await fetchManifestJournal(siteID))
  }, [])

  // Full reload when the site changes: stored operator plan → draft,
  // then preview + journal.
  useEffect(() => {
    if (!site) return
    let cancelled = false
    setError('')
    setNotice('')
    setPreview(null)
    ;(async () => {
      try {
        const stored = await fetchLoadPlan(site)
        if (cancelled) return
        const d = new Map(stored.map((e) => [e.ts, e.load_kw]))
        setDraft(d)
        setDirty(false)
        await refreshPreview(site, d)
        await refreshJournal(site)
      } catch (e) {
        if (!cancelled) setError(String(e))
      }
    })()
    return () => {
      cancelled = true
    }
  }, [site, refreshPreview, refreshJournal])

  // Journal auto-refresh while the page is open (poll delivery status).
  useEffect(() => {
    if (!site) return
    const id = window.setInterval(() => {
      refreshJournal(site).catch(() => {})
    }, 30_000)
    return () => window.clearInterval(id)
  }, [site, refreshJournal])

  const schedulePreview = useCallback(
    (siteID: string, draftMap: Map<string, number>) => {
      if (previewTimer.current) window.clearTimeout(previewTimer.current)
      previewTimer.current = window.setTimeout(() => {
        refreshPreview(siteID, draftMap).catch((e) => setError(String(e)))
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
    setBusy(true)
    setError('')
    try {
      await saveLoadPlan(site, draftToEntries(draft))
      setDirty(false)
      setNotice('План споживання збережено.')
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
      if (dirty) {
        await saveLoadPlan(site, draftToEntries(draft))
        setDirty(false)
      }
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

  return (
    <div className="planner-page">
      <header className="planner-header-row planner-header">
        <div>
          <h1>План на добу</h1>
          <p>Плануємо наперед: від поточної години до кінця відомих цін РДН.</p>
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

      {error && <div className="planner-error">{error}</div>}
      {notice && !error && <div className="planner-card planner-card-sub">{notice}</div>}

      {loading && <div className="planner-empty">Завантаження…</div>}

      {!loading && sites.length === 0 && !error && (
        <div className="planner-card">
          <h2>Edge-обʼєкти не налаштовані</h2>
          <p className="planner-card-sub">
            Планувальник працює для обʼєктів з увімкненим edge-контуром (env EDGE_SITE_TOKENS на
            сервері). Після налаштування тут зʼявиться вибір обʼєкта.
          </p>
        </div>
      )}

      {preview && (
        <>
          <div className="planner-card">
            <div className="planner-status-line">
              <span>
                SOC зараз: <b>{preview.params.start_soc_pct.toFixed(0)}%</b>
              </span>
              <span>
                УЗЕ: <b>{preview.params.power_kw.toFixed(0)} кВт</b> /{' '}
                <b>{preview.params.capacity_kwh.toFixed(0)} кВт·год</b>
              </span>
              <span>
                Економічна зона SOC:{' '}
                <b>
                  {preview.params.soc_min_pct}–{preview.params.soc_max_pct}%
                </b>
              </span>
              <span>
                Load:{' '}
                <span
                  className={
                    'planner-chip ' +
                    (preview.load_source.startsWith('operator') ? 'operator' : 'heuristic')
                  }
                >
                  {LOAD_SOURCE_LABEL[preview.load_source] ?? preview.load_source}
                </span>
              </span>
              <span>
                Годин з цінами РДН: <b>{pricedHours}</b> з {preview.hours.length}
                {pricedHours < preview.hours.length &&
                  ' (без цін — edge діє за preset-правилами)'}
              </span>
            </div>
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
              Завтра: load <b>{Math.round(tomorrowLoadKwh).toLocaleString('uk-UA')} кВт·год</b>
            </span>
            <span>
              СЕС (прогноз) <b>{Math.round(tomorrowPvKwh).toLocaleString('uk-UA')} кВт·год</b>
            </span>
            <span style={{ marginLeft: 'auto', display: 'inline-flex', gap: 8 }}>
              <button
                type="button"
                className="planner-button"
                onClick={savePlan}
                disabled={busy || !dirty}
              >
                Зберегти план
              </button>
              <button
                type="button"
                className="planner-button planner-button-primary"
                onClick={publish}
                disabled={busy}
              >
                Опублікувати на edge
              </button>
            </span>
          </div>

          <ContextChart preview={preview} />
          <PlanChart preview={preview} />
          {tomorrow && <EffectWaterfall day={tomorrow} dateLabel={tomorrowLabel} />}
        </>
      )}

      {journal && <ManifestJournal journal={journal} />}
    </div>
  )
}
