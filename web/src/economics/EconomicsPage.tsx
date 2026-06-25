import { useCallback, useEffect, useMemo, useState } from 'react'
import { refreshDAMPrices } from '../api'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { dailyTotals } from './compute'
import { EconomicsDamPricesModal } from './components/EconomicsDamPricesModal'
import { EconomicsHeader, type EconomicsRange } from './components/EconomicsHeader'
import { EconomicsKpis } from './components/EconomicsKpis'
import { EconomicsRecomputeModal } from './components/EconomicsRecomputeModal'
import { EconomicsTable } from './components/EconomicsTable'
import { EconomicsMonthlyView } from './monthly/EconomicsMonthlyView'
import { EconomicsAnnualView } from './annual/EconomicsAnnualView'
import './economics.css'
import { useEconomicsData } from './useEconomicsData'
import { useEconomicsMonthlyData } from './useEconomicsMonthlyData'
import { useEconomicsAnnualData } from './useEconomicsAnnualData'
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

// readRangeFromUrl picks the period granularity (day / month / year).
// Defaults to 'day' so existing links and the common case stay unchanged.
function readRangeFromUrl(): EconomicsRange {
  if (typeof window === 'undefined') return 'day'
  const params = new URLSearchParams(window.location.search)
  const raw = params.get('range')
  if (raw === 'month') return 'month'
  if (raw === 'year') return 'year'
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
  if (range === 'month' || range === 'year') {
    url.searchParams.set('range', range)
  } else {
    url.searchParams.delete('range')
  }
  if (range === 'year' && windowFrom && windowTo) {
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

  const onBackToDashboard = useCallback(() => {
    if (typeof window === 'undefined') return
    const url = new URL(window.location.href)
    url.searchParams.delete('view')
    // We deliberately leave the date in the URL: returning to the
    // economics page later restores the operator's last working day
    // without forcing them to retype it.
    window.history.pushState({}, '', url.toString())
    window.dispatchEvent(new PopStateEvent('popstate'))
  }, [])

  return (
    <main className="economics-page">
      <EconomicsHeader
        organizationID={organizationID}
        organizationOptions={options}
        onOrganizationChange={onOrganizationChange}
        range={range}
        onRangeChange={setRange}
        monthsWithData={range === 'year' ? annual.year?.months_with_data : undefined}
        windowFrom={windowFrom || (range === 'year' ? `${period}-01` : '')}
        windowTo={windowTo || (range === 'year' ? `${period}-12` : '')}
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
        onBackToDashboard={onBackToDashboard}
        onRefreshDam={onRefreshDam}
        damRefreshState={damRefreshState}
        damRefreshError={damRefreshError}
        onOpenRecompute={() => setRecomputeOpen(true)}
        onOpenDamImport={() => setDamImportOpen(true)}
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

      {range === 'year' ? (
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
            <EconomicsMonthlyView data={monthly.month} organizationID={organizationID} />
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
              <EconomicsKpis totals={totals} tariffs={tariffs} />
              <EconomicsTable rows={data.rows} organizationID={organizationID} date={date} />
            </>
          )}
        </>
      )}
    </main>
  )
}
