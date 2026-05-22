import { useCallback, useEffect, useMemo, useState } from 'react'
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

function readDateFromUrl(): string {
  if (typeof window === 'undefined') return today()
  const params = new URLSearchParams(window.location.search)
  const value = params.get('date')
  if (!value) return today()
  // Soft validation: must look like YYYY-MM-DD. We don't full-parse
  // here — the backend already rejects malformed dates with a 400.
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return today()
  return value
}

// updateUrl rewrites the current URL's query string in place using
// `replaceState` so changing the date doesn't pollute the browser
// history with one entry per keystroke. Tariffs are no longer URL-
// persisted (per-org settings live on the backend now); the org param
// is owned by `useOrganizationParam`. Any leftover legacy tariff
// query params (distribution=…, etc.) are silently dropped here so
// shared old links self-clean on first interaction.
const LEGACY_TARIFF_KEYS = [
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
  url.searchParams.set('date', date)
  for (const key of LEGACY_TARIFF_KEYS) url.searchParams.delete(key)
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

  const data = useEconomicsData({ organizationID, date, tariffs })

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
          <EconomicsTable rows={data.rows} />
        </>
      )}
    </main>
  )
}
