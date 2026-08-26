// ControlPage — the «Керування» mode: the same object as the analytics
// dashboard, but through the manifest lens. Tabs follow the approved
// design: Стан (live + план і факт) · План УЗЕ (planner wizard) ·
// Режими (presets + ручний план) · Обмеження (settings) · Журнал.

import { useCallback, useEffect, useMemo, useState } from 'react'
import '../dashboard/dashboard.css'
import './control.css'
import { PlannerPage } from '../planner/PlannerPage'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { fetchEdgeSites } from '../planner/plannerClient'
import { fetchEdgeStatus, type EdgeSiteStatus } from './controlClient'
import { JournalTab } from './JournalTab'
import { ModeTopBar, type TopBarMenuItem } from '../shell/ModeTopBar'
import { ModesTab } from './ModesTab'
import { SettingsTab } from './SettingsTab'
import { StateTab } from './StateTab'

type Tab = 'state' | 'plan' | 'modes' | 'limits' | 'journal'

const TAB_LABELS: { id: Tab; label: string }[] = [
  { id: 'state', label: 'Стан' },
  { id: 'plan', label: 'План УЗЕ' },
  { id: 'modes', label: 'Режими' },
  { id: 'limits', label: 'Обмеження' },
  { id: 'journal', label: 'Журнал' },
]

const STATUS_POLL_MS = 15_000

function readTab(): Tab {
  const t = new URLSearchParams(window.location.search).get('tab')
  if (t === 'plan' || t === 'modes' || t === 'limits' || t === 'journal') return t
  return 'state'
}

export function ControlPage() {
  const { organizationID, change: changeOrganization } = useOrganizationParam()
  const [sites, setSites] = useState<string[]>([])
  const [sitesLoaded, setSitesLoaded] = useState(false)
  const [tab, setTab] = useState<Tab>(readTab)
  const [status, setStatus] = useState<EdgeSiteStatus | null>(null)

  useEffect(() => {
    let cancelled = false
    fetchEdgeSites()
      .then((list) => {
        if (cancelled) return
        setSites(list)
        // The control mode only makes sense for edge sites; if the
        // dashboard was on a non-edge org, jump to the first edge site.
        if (list.length > 0 && !list.includes(organizationID)) {
          changeOrganization(list[0])
        }
      })
      .catch(() => {})
      .finally(() => !cancelled && setSitesLoaded(true))
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const site = sites.includes(organizationID) ? organizationID : ''

  const refreshStatus = useCallback(() => {
    if (!site) return
    fetchEdgeStatus(site, 100)
      .then(setStatus)
      .catch(() => {})
  }, [site])

  useEffect(() => {
    setStatus(null)
    if (!site) return
    refreshStatus()
    const id = window.setInterval(refreshStatus, STATUS_POLL_MS)
    return () => window.clearInterval(id)
  }, [site, refreshStatus])

  useEffect(() => {
    const handler = () => setTab(readTab())
    window.addEventListener('popstate', handler)
    return () => window.removeEventListener('popstate', handler)
  }, [])

  const switchTab = (next: Tab) => {
    setTab(next)
    const url = new URL(window.location.href)
    if (next === 'state') url.searchParams.delete('tab')
    else url.searchParams.set('tab', next)
    window.history.replaceState({}, '', url)
  }

  const goTo = (view: string) => {
    const url = new URL(window.location.href)
    url.searchParams.set('view', view)
    url.searchParams.delete('tab')
    window.history.pushState({}, '', url)
    window.dispatchEvent(new PopStateEvent('popstate'))
  }

  const siteOptions = useMemo(() => (sites.length > 0 ? sites : [organizationID]), [sites, organizationID])

  // Cross-page navigation only; the planner and the journal live in
  // the tabs, so no menu entries for them.
  const serviceMenu: TopBarMenuItem[] = [
    { id: 'station', label: 'Паспорт станції', onSelect: () => goTo('station') },
    { id: 'alerts', label: 'Сповіщення', onSelect: () => goTo('alerts') },
  ]

  return (
    <main className="dashboard-page">
      <ModeTopBar
        mode="control"
        organizationID={organizationID}
        options={siteOptions}
        onOrganizationChange={changeOrganization}
        menu={serviceMenu}
        status={status}
      />

      <nav className="ctl-tabs" aria-label="Розділи керування">
        {TAB_LABELS.map((t) => (
          <button
            key={t.id}
            type="button"
            className={tab === t.id ? 'active' : ''}
            onClick={() => switchTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      {!site ? (
        <div className="ctl-card">
          <div className="ctl-placeholder">
            {sitesLoaded
              ? 'Для цього обʼєкта немає edge-пристрою. Оберіть обʼєкт з IOT2050 у списку зверху.'
              : 'Завантаження…'}
          </div>
        </div>
      ) : tab === 'state' ? (
        <StateTab site={site} status={status} />
      ) : tab === 'plan' ? (
        <PlannerPage embedded siteOverride={site} />
      ) : tab === 'modes' ? (
        <ModesTab site={site} status={status} onChanged={refreshStatus} />
      ) : tab === 'limits' ? (
        <SettingsTab site={site} onChanged={refreshStatus} />
      ) : (
        <JournalTab site={site} status={status} />
      )}
    </main>
  )
}
