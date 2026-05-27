import { useCallback, useEffect, useMemo, useState } from 'react'
import { refreshDAMPrices } from '../api'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { dailyTotals } from './compute'
import { EconomicsCharts } from './components/EconomicsCharts'
import { EconomicsHeader } from './components/EconomicsHeader'
import { EconomicsKpis } from './components/EconomicsKpis'
import { EconomicsRevenuePanel } from './components/EconomicsRevenuePanel'
import { EconomicsTable } from './components/EconomicsTable'
import './economics.css'
import { useEconomicsData } from './useEconomicsData'
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

function updateUrl(date: string) {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  url.searchParams.set('view', 'economics')
  url.searchParams.set('anchor', date)
  for (const key of LEGACY_QUERY_KEYS) url.searchParams.delete(key)
  window.history.replaceState({}, '', url.toString())
}

export function EconomicsPage() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const [date, setDate] = useState<string>(readDateFromUrl)
  const {
    tariffs,
    status: tariffsStatus,
    error: tariffsError,
    setTariffs,
  } = useOrgTariffs(organizationID)

  // Keep the URL in sync with the date so the analyst can copy/paste
  // a "this view" link into Slack. Org id changes are already URL-
  // synced inside `useOrganizationParam`; tariffs are persisted per-
  // org on the backend and intentionally don't show up in the URL.
  useEffect(() => {
    updateUrl(date)
  }, [date])

  // refreshKey bumps every time a successful POST to
  // /api/v1/dam-prices/refresh comes back, forcing useEconomicsData
  // to re-fire all 8 underlying GETs so the dashboard picks up the
  // newly-stored RDN prices without a full page reload.
  const [refreshKey, setRefreshKey] = useState(0)
  const [damRefreshState, setDamRefreshState] = useState<DamRefreshState>('idle')
  const [damRefreshError, setDamRefreshError] = useState<string | null>(null)

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

  const data = useEconomicsData({ organizationID, date, tariffs, refreshKey })

  const totals = useMemo(() => dailyTotals(data.rows), [data.rows])

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
      />

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

      {data.loading ? (
        <p className="economics-loading">Завантаження…</p>
      ) : (
        <>
          <EconomicsKpis totals={totals} tariffs={tariffs} />
          <EconomicsRevenuePanel totals={totals} />
          <EconomicsCharts rows={data.rows} />
          <EconomicsTable rows={data.rows} organizationID={organizationID} date={date} />
        </>
      )}
    </main>
  )
}
