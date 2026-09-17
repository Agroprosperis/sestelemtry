import { useCallback, useEffect, useMemo, useState } from 'react'
import { refreshDAMPrices } from '../api'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar, type TopBarMenuItem } from '../shell/ModeTopBar'
import { dailyTotals, pvEssArbitrageGain } from './compute'
import { EconomicsDamPricesModal } from './components/EconomicsDamPricesModal'
import { EconomicsHeader, type EconomicsRange } from './components/EconomicsHeader'
import { EconomicsKpis } from './components/EconomicsKpis'
import { EconomicsRecomputeModal } from './components/EconomicsRecomputeModal'
import { EconomicsTable } from './components/EconomicsTable'
import { EconomicsMonthlyView } from './monthly/EconomicsMonthlyView'
import { EconomicsAnnualView } from './annual/EconomicsAnnualView'
import { EconomicsPaybackView } from './payback/EconomicsPaybackView'
import { EconomicsPortfolioView, type PortfolioScope } from './portfolio/EconomicsPortfolioView'
import type { EconomicsPortfolioSite } from '../api'
import './economics.css'
import { useEconomicsData } from './useEconomicsData'
import { useEconomicsMonthlyData } from './useEconomicsMonthlyData'
import { useEconomicsAnnualData } from './useEconomicsAnnualData'
import { usePvPlanSummary } from './usePvPlanSummary'
import { useCapexSchedule } from './useCapexSchedule'
import { capexResolver } from './payback'
import { useOrgTariffs } from './useOrgTariffs'

// DamRefreshState is the small UI state machine that drives the
// "Оновити ціни РДН" button and its status hint: idle (clickable),
// loading (in-flight POST), error (the most recent attempt failed —
// the button is clickable again, but the error message lives in
// the title tooltip so the operator can read what went wrong).
type DamRefreshState = 'idle' | 'loading' | 'error'

// today() returns YYYY-MM-DD in Europe/Kyiv. The economics page is
// always anchored to local Ukraine time regardless of the operator's
// browser zone — DAM hours, distribution tariffs and the existing
// dashboard cards all use this convention, and a mixed UTC/local
// view would silently drift the daily totals at the timezone seam.
function today(): string {
  const fmt = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Europe/Kyiv',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
  return fmt.format(new Date())
}

// readDateFromUrl picks the day shown on the economics page,
// preferring `?anchor` so toggling between the main dashboard and
// this view keeps a shared "what day am I looking at" pin without
// the operator re-selecting it. `?date` is supported as a
// backward-compat fallback for shared links from before the URL
// keys were unified — they self-heal on first edit because
// `updateUrl` deletes the legacy `date` param when it writes
// `anchor`.
//
// We accept any preset's anchor (day/month/year) as the date here:
// month/year anchors land on the 1st of the period, which is a
// valid calendar day for the economics view to render.
function readDateFromUrl(): string {
  if (typeof window === 'undefined') return today()
  const params = new URLSearchParams(window.location.search)
  const raw = (params.get('anchor') ?? params.get('date') ?? '').trim()
  if (!raw) return today()
  // Soft validation: must look like YYYY-MM-DD. We don't full-parse
  // here — the backend already rejects malformed dates with a 400.
  if (!/^\d{4}-\d{2}-\d{2}$/.test(raw)) return today()
  return raw
}

// updateUrl rewrites the current URL's query string in place using
// `replaceState` so changing the date doesn't pollute the browser
// history with one entry per keystroke. Tariffs are no longer URL-
// persisted (per-org settings live on the backend now); the org param
// is owned by `useOrganizationParam`. We write the same `anchor`
// key the main dashboard uses so a click on "← Дашборд" lands on
// the same day the analyst was viewing here.
//
// Legacy keys (`?date` from older economics shares, `?distribution=…`
// etc. from URL-persisted tariffs) are silently dropped so shared
// old links self-clean on first interaction.
const LEGACY_QUERY_KEYS = [
  'date',
  'distribution',
  'transmission',
  'supplier_margin',
  'other_fees',
  'export_discount',
  'degradation',
  'include_vat',
  'vat_rate',
  'ess_capacity',
]

// PAYBACK_WINDOW_FROM anchors the payback page's all-time fetch. The
// earliest imported archive (АСКОЕ Жмеринка) starts 2024-08; months
// before the first data month come back empty and are skipped.
const PAYBACK_WINDOW_FROM = '2024-08'

// addMonths shifts a YYYY-MM month key by delta calendar months. Pure
// string/index arithmetic — no Date, so no UTC-midnight drift.
function addMonths(ym: string, delta: number): string {
  const m = /^(\d{4})-(\d{2})$/.exec(ym)
  if (!m) return ym
  const idx = Number(m[1]) * 12 + (Number(m[2]) - 1) + delta
  const y = Math.floor(idx / 12)
  const mo = idx - y * 12 + 1
  return `${String(y).padStart(4, '0')}-${String(mo).padStart(2, '0')}`
}

// monthsBetween counts the inclusive month length of a YYYY-MM window,
// so the previous-period comparison can mirror the window exactly.
function monthsBetween(from: string, to: string): number {
  const f = /^(\d{4})-(\d{2})$/.exec(from)
  const t = /^(\d{4})-(\d{2})$/.exec(to)
  if (!f || !t) return 0
  return Number(t[1]) * 12 + Number(t[2]) - (Number(f[1]) * 12 + Number(f[2])) + 1
}

// monthEndDay returns the YYYY-MM-DD of the last civil day of a YYYY-MM
// month (Date.UTC day 0 of the next month).
function monthEndDay(ym: string): string {
  const m = /^(\d{4})-(\d{2})$/.exec(ym)
  if (!m) return ''
  const last = new Date(Date.UTC(Number(m[1]), Number(m[2]), 0)).getUTCDate()
  return `${ym}-${String(last).padStart(2, '0')}`
}

// readRangeFromUrl picks the period granularity (day / month / year).
// Defaults to 'day' so existing links and the common case stay unchanged.
function readRangeFromUrl(): EconomicsRange {
  if (typeof window === 'undefined') return 'day'
  const params = new URLSearchParams(window.location.search)
  const raw = params.get('range')
  if (raw === 'month') return 'month'
  if (raw === 'year') return 'year'
  if (raw === 'payback') return 'payback'
  if (raw === 'portfolio') return 'portfolio'
  return 'day'
}

// readWindowFromUrl picks the optional sliding-period window (year view).
// Both `from` and `to` must be present and look like YYYY-MM, otherwise
// the year view falls back to the calendar year of the anchor.
function readWindowFromUrl(): { from: string; to: string } {
  if (typeof window === 'undefined') return { from: '', to: '' }
  const params = new URLSearchParams(window.location.search)
  const from = (params.get('from') ?? '').trim()
  const to = (params.get('to') ?? '').trim()
  if (/^\d{4}-\d{2}$/.test(from) && /^\d{4}-\d{2}$/.test(to)) return { from, to }
  return { from: '', to: '' }
}

function updateUrl(date: string, range: EconomicsRange, windowFrom: string, windowTo: string) {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  url.searchParams.set('view', 'economics')
  url.searchParams.set('anchor', date)
  if (range === 'month' || range === 'year' || range === 'payback' || range === 'portfolio') {
    url.searchParams.set('range', range)
  } else {
    url.searchParams.delete('range')
  }
  if ((range === 'year' || range === 'portfolio') && windowFrom && windowTo) {
    url.searchParams.set('from', windowFrom)
    url.searchParams.set('to', windowTo)
  } else {
    url.searchParams.delete('from')
    url.searchParams.delete('to')
  }
  for (const key of LEGACY_QUERY_KEYS) url.searchParams.delete(key)
  window.history.replaceState({}, '', url.toString())
}

export function EconomicsPage() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const [date, setDate] = useState<string>(readDateFromUrl)
  const [range, setRange] = useState<EconomicsRange>(readRangeFromUrl)
  const initialWindow = readWindowFromUrl()
  const [windowFrom, setWindowFrom] = useState<string>(initialWindow.from)
  const [windowTo, setWindowTo] = useState<string>(initialWindow.to)
  // Portfolio granularity (month/year) lives here so the header period
  // picker can follow the portfolio's own Місяць/Рік toggle.
  const [portfolioScope, setPortfolioScope] = useState<PortfolioScope>('month')
  const {
    tariffs,
    status: tariffsStatus,
    error: tariffsError,
    setTariffs,
  } = useOrgTariffs(organizationID)

  // Keep the URL in sync with the date + range so the analyst can
  // copy/paste a "this view" link into Slack. Org id changes are
  // already URL-synced inside `useOrganizationParam`; tariffs are
  // persisted per-org on the backend and intentionally don't show up
  // in the URL.
  useEffect(() => {
    updateUrl(date, range, windowFrom, windowTo)
  }, [date, range, windowFrom, windowTo])

  // refreshKey bumps every time a successful POST to
  // /api/v1/dam-prices/refresh comes back, forcing useEconomicsData
  // to re-fire all 8 underlying GETs so the dashboard picks up the
  // newly-stored RDN prices without a full page reload.
  const [refreshKey, setRefreshKey] = useState(0)
  const [damRefreshState, setDamRefreshState] = useState<DamRefreshState>('idle')
  const [damRefreshError, setDamRefreshError] = useState<string | null>(null)
  const [recomputeOpen, setRecomputeOpen] = useState(false)
  const [damImportOpen, setDamImportOpen] = useState(false)

  const onRefreshDam = useCallback(async () => {
    setDamRefreshState('loading')
    setDamRefreshError(null)
    try {
      await refreshDAMPrices({ date })
      setRefreshKey((k) => k + 1)
      setDamRefreshState('idle')
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setDamRefreshError(msg)
      setDamRefreshState('error')
    }
  }, [date])

  // Only the active view fetches: passing an empty org id makes the
  // hook stay idle (its effect short-circuits), so toggling Day/Month
  // never hits both the daily and the monthly endpoint at once.
  const data = useEconomicsData({
    organizationID: range === 'day' ? organizationID : '',
    date: range === 'day' ? date : '',
    tariffs,
    refreshKey,
  })

  const totals = useMemo(() => dailyTotals(data.rows), [data.rows])
  const pvEssPotential = useMemo(
    () => totals.pvExportPotential + pvEssArbitrageGain(data.rows, tariffs),
    [totals, data.rows, tariffs],
  )

  // month is the YYYY-MM derived from the day anchor.
  const month = date.slice(0, 7)
  const monthly = useEconomicsMonthlyData({
    organizationID: range === 'month' ? organizationID : '',
    month: range === 'month' ? month : '',
    refreshKey,
  })

  // period is the YYYY calendar year derived from the day anchor; an
  // explicit from/to window (if set) overrides it in the year view.
  const period = date.slice(0, 4)
  const useWindow = range === 'year' && Boolean(windowFrom && windowTo)
  const annual = useEconomicsAnnualData({
    organizationID: range === 'year' ? organizationID : '',
    period: range === 'year' && !useWindow ? period : '',
    from: useWindow ? windowFrom : '',
    to: useWindow ? windowTo : '',
    refreshKey,
  })

  // Previous periods of the same length feed the "до попереднього …"
  // deltas on the redesigned top cards. Loaded through the same hooks
  // as the main period; their errors are never surfaced — an absent
  // prior period only hides the deltas.
  const monthlyPrior = useEconomicsMonthlyData({
    organizationID: range === 'month' ? organizationID : '',
    month: range === 'month' ? addMonths(month, -1) : '',
    refreshKey,
  })

  const windowLen = useWindow ? monthsBetween(windowFrom, windowTo) : 12
  const annualPrior = useEconomicsAnnualData({
    organizationID: range === 'year' ? organizationID : '',
    period: range === 'year' && !useWindow ? String(Number(period) - 1) : '',
    from: range === 'year' && useWindow ? addMonths(windowFrom, -windowLen) : '',
    to: range === 'year' && useWindow ? addMonths(windowFrom, -1) : '',
    refreshKey,
  })

  // Planned PV generation of the visible period for the «Генерація СЕС»
  // card (best-effort; null hides the plan line).
  const pvPlanMonth = usePvPlanSummary({
    organizationID: range === 'month' ? organizationID : '',
    fromDay: range === 'month' ? `${month}-01` : '',
    toDay: range === 'month' ? monthEndDay(month) : '',
    refreshKey,
  })
  const yearFromMonth = useWindow ? windowFrom : `${period}-01`
  const yearToMonth = useWindow ? windowTo : `${period}-12`
  const pvPlanYear = usePvPlanSummary({
    organizationID: range === 'year' ? organizationID : '',
    fromDay: range === 'year' ? `${yearFromMonth}-01` : '',
    toDay: range === 'year' ? monthEndDay(yearToMonth) : '',
    refreshKey,
  })

  // The payback page always looks at the whole operating history: a
  // fixed window from before the earliest site went live up to the
  // current month. Anything even earlier still arrives aggregated in
  // prior_ebitda_uah / prior_months_with_data.
  const payback = useEconomicsAnnualData({
    organizationID: range === 'payback' ? organizationID : '',
    period: '',
    from: range === 'payback' ? PAYBACK_WINDOW_FROM : '',
    to: range === 'payback' ? today().slice(0, 7) : '',
    refreshKey,
  })

  // Staged projects grow their CAPEX over time, and each step is already
  // versioned in the tariff schedule; the payback page compares the
  // cumulative EBITDA against the CAPEX standing in each month, and the
  // month/year top section divides the annualised EBITDA by the capital
  // standing at the end of its period (ROCE card).
  const capexSteps = useCapexSchedule(
    range === 'payback' || range === 'month' || range === 'year' ? organizationID : '',
    refreshKey,
  )
  const capexAt = useMemo(() => capexResolver(capexSteps, tariffs.capexUah), [capexSteps, tariffs.capexUah])

  // jumpToMonth switches the page to the month view of the given YYYY-MM
  // (drill-down from an annual trend bar / table row). We keep the day
  // anchor at the first of that month so the month picker lands cleanly.
  const jumpToMonth = useCallback(
    (monthStr: string) => {
      if (!/^\d{4}-\d{2}$/.test(monthStr)) return
      setDate(`${monthStr}-01`)
      setRange('month')
    },
    [setDate, setRange],
  )

  // diagnoseBessFromPortfolio opens the flagged object at the first
  // anomalous УЗЕ day (day view) so the operator can inspect hourly
  // charge/discharge. Without concrete dates, fall back to the matching
  // month/year view and scroll to the УЗЕ data-quality note.
  const diagnoseBessFromPortfolio = useCallback(
    (site: EconomicsPortfolioSite) => {
      onOrganizationChange(site.id)
      const dates = (site.bess_anomalous_dates ?? []).filter((d) => /^\d{4}-\d{2}-\d{2}$/.test(d))
      if (dates.length > 0) {
        const first = [...dates].sort()[0]
        setDate(first)
        setRange('day')
        return
      }
      if (portfolioScope === 'month') {
        setDate(`${month}-01`)
        setRange('month')
      } else {
        setRange('year')
      }
      window.setTimeout(() => {
        document.getElementById('ess-data-quality')?.scrollIntoView({ behavior: 'smooth', block: 'center' })
        document.getElementById('ess-data-quality')?.focus?.()
      }, 400)
    },
    [onOrganizationChange, portfolioScope, month],
  )

  // The economics-specific admin actions live in the shell's «Сервіс»
  // menu (the DAM buttons left the header toolbar).
  const serviceMenu: TopBarMenuItem[] = [
    { id: 'dam-refresh', label: 'Оновити ціни РДН', onSelect: () => void onRefreshDam() },
    { id: 'dam-import', label: 'Імпорт цін РДН', onSelect: () => setDamImportOpen(true) },
    { id: 'recompute', label: 'Перерахунок економіки', onSelect: () => setRecomputeOpen(true) },
  ]

  return (
    <main className="economics-page">
      <ModeTopBar
        mode="economics"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={onOrganizationChange}
        menu={serviceMenu}
      />

      {/* The menu closes on click, so the refresh lifecycle needs its
          own strip: progress while the POST runs, the upstream message
          when it fails. */}
      {damRefreshState === 'loading' && (
        <div className="economics-dam-status" role="status">
          Оновлюємо ціни РДН з OREE…
        </div>
      )}
      {damRefreshState === 'error' && (
        <div className="economics-dam-status economics-dam-status-error" role="alert">
          Не вдалося оновити ціни РДН{damRefreshError ? `: ${damRefreshError}` : ''}
        </div>
      )}

      <EconomicsHeader
        organizationID={organizationID}
        range={range}
        onRangeChange={setRange}
        portfolioScope={portfolioScope}
        windowFrom={windowFrom || (range === 'year' || range === 'portfolio' ? `${period}-01` : '')}
        windowTo={windowTo || (range === 'year' || range === 'portfolio' ? `${period}-12` : '')}
        onWindowChange={(nextFrom, nextTo) => {
          setWindowFrom(nextFrom)
          setWindowTo(nextTo)
        }}
        date={date}
        onDateChange={setDate}
        tariffs={tariffs}
        onTariffsChange={setTariffs}
        tariffsStatus={tariffsStatus}
        tariffsError={tariffsError}
      />

      {recomputeOpen && (
        <EconomicsRecomputeModal
          onClose={() => setRecomputeOpen(false)}
          organizationOptions={options}
          initialOrganizationID={organizationID}
          onDone={() => setRefreshKey((k) => k + 1)}
        />
      )}

      {damImportOpen && (
        <EconomicsDamPricesModal
          onClose={() => setDamImportOpen(false)}
          onDone={() => setRefreshKey((k) => k + 1)}
        />
      )}

      {range === 'portfolio' ? (
        <EconomicsPortfolioView
          month={month}
          period={period}
          from={windowFrom || `${period}-01`}
          to={windowTo || `${period}-12`}
          scope={portfolioScope}
          onScopeChange={setPortfolioScope}
          onDiagnoseBess={diagnoseBessFromPortfolio}
          refreshKey={refreshKey}
        />
      ) : range === 'payback' ? (
        <>
          {payback.error && (
            <section className="economics-banner economics-banner-error" role="alert">
              Не вдалося завантажити дані: {payback.error}
            </section>
          )}
          {payback.loading ? (
            <p className="economics-loading">Завантаження…</p>
          ) : payback.year ? (
            <EconomicsPaybackView
              data={payback.year}
              capexUah={tariffs.capexUah}
              capexSteps={capexSteps}
              plannedPaybackMonths={tariffs.plannedPaybackMonths}
            />
          ) : (
            <p className="economics-loading">Немає даних.</p>
          )}
        </>
      ) : range === 'year' ? (
        <>
          {annual.error && (
            <section className="economics-banner economics-banner-error" role="alert">
              Не вдалося завантажити дані: {annual.error}
            </section>
          )}
          {annual.loading ? (
            <p className="economics-loading">Завантаження…</p>
          ) : annual.year ? (
            <EconomicsAnnualView
              data={annual.year}
              organizationID={organizationID}
              onSelectMonth={jumpToMonth}
              prior={
                annualPrior.year && annualPrior.year.totals.hours_with_data > 0
                  ? annualPrior.year.totals
                  : null
              }
              pvPlan={pvPlanYear}
              capexUah={capexAt(yearToMonth)}
            />
          ) : (
            <p className="economics-loading">Немає даних за рік.</p>
          )}
        </>
      ) : range === 'month' ? (
        <>
          {monthly.error && (
            <section className="economics-banner economics-banner-error" role="alert">
              Не вдалося завантажити дані: {monthly.error}
            </section>
          )}
          {monthly.loading ? (
            <p className="economics-loading">Завантаження…</p>
          ) : monthly.month ? (
            <EconomicsMonthlyView
              data={monthly.month}
              organizationID={organizationID}
              prior={
                monthlyPrior.month && monthlyPrior.month.totals.hours_with_data > 0
                  ? monthlyPrior.month.totals
                  : null
              }
              pvPlan={pvPlanMonth}
              capexUah={capexAt(month)}
            />
          ) : (
            <p className="economics-loading">Немає даних за місяць.</p>
          )}
        </>
      ) : (
        <>
          {data.error && (
            <section className="economics-banner economics-banner-error" role="alert">
              Не вдалося завантажити дані: {data.error}
            </section>
          )}

          {!data.error && data.hoursMissingPrice > 0 && (
            <section className="economics-banner" role="status">
              Ціни РДН частково відсутні: {data.hoursMissingPrice} год без ціни.
              Розрахунок ефекту виконано лише для годин з відомою ціною.
            </section>
          )}

          {!data.error && data.skipDiagnostics && (
            <section className="economics-banner" role="status">
              Алокатор повідомив про неповні дані: {data.skipDiagnostics}
            </section>
          )}

          {!data.error && !data.loading && data.reconciled && (
            <section className="economics-banner economics-banner-ok" role="status">
              <span className="economics-reconciled-badge">Звірено з FusionSolar</span>
              Денні підсумки масштабовано під канонічні KPI станції.
              {data.qualityFlags.length > 0 && (
                <> Розбіжності: {data.qualityFlags.join(', ')}.</>
              )}
            </section>
          )}

          {data.loading ? (
            <p className="economics-loading">Завантаження…</p>
          ) : (
            <>
              <EconomicsKpis totals={totals} tariffs={tariffs} pvEssPotential={pvEssPotential} />
              <EconomicsTable rows={data.rows} organizationID={organizationID} date={date} />
            </>
          )}
        </>
      )}
    </main>
  )
}
