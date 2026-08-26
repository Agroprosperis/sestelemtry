// ModeTopBar is the app-wide strip above the analytics dashboard, the
// «Керування» view and the service pages (station passport, import):
// the single object picker + mode switch + edge status chips (IOT2050
// online, active mode, manifest delivery state).

import { useEffect, useState } from 'react'
import { fetchEdgeFleet, type EdgeSiteStatus } from '../control/controlClient'
import { formatOrganizationLabel } from '../dashboard/config'
import './shell.css'

// 'none' is for the service pages (station, import): neither mode is
// highlighted, both buttons navigate.
export type ConsoleMode = 'analytics' | 'control' | 'none'

// navigateView switches between the two modes without a full reload,
// preserving the rest of the query string (organization_id etc).
export function navigateView(view: 'dashboard' | 'control', tab?: string) {
  const url = new URL(window.location.href)
  if (view === 'dashboard') url.searchParams.delete('view')
  else url.searchParams.set('view', view)
  if (tab) url.searchParams.set('tab', tab)
  else url.searchParams.delete('tab')
  window.history.pushState({}, '', url)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

type Props = {
  mode: ConsoleMode
  organizationID: string
  options: string[]
  onOrganizationChange: (next: string) => void
  // title renders the brand block (logo + page title) inside the bar,
  // so the whole shell fits one row instead of two.
  title?: string
  // status lets the control page share its own poll; when omitted the
  // bar quietly polls the fleet itself.
  status?: EdgeSiteStatus | null
}

const FLEET_POLL_MS = 60_000

function shortManifestID(id: string): string {
  // ze-20260826-a1b2c3d4 → ZE-a1b2 (mock: «Manifest ZE-0642»)
  const parts = id.split('-')
  const site = (parts[0] ?? '').toUpperCase()
  const hash = parts[parts.length - 1] ?? ''
  return `${site}-${hash.slice(0, 4)}`
}

export function ModeTopBar({
  mode,
  organizationID,
  options,
  onOrganizationChange,
  title,
  status,
}: Props) {
  const [fleetStatus, setFleetStatus] = useState<EdgeSiteStatus | null>(null)

  // Without a shared poll the bar checks the fleet itself, so any page
  // can show whether the selected object has an edge device online.
  useEffect(() => {
    if (status !== undefined) return
    let cancelled = false
    const load = () =>
      fetchEdgeFleet()
        .then((f) => {
          if (cancelled) return
          setFleetStatus((f.sites ?? []).find((s) => s.site_id === organizationID) ?? null)
        })
        .catch(() => !cancelled && setFleetStatus(null))
    void load()
    const id = window.setInterval(load, FLEET_POLL_MS)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [status, organizationID])

  const st = status !== undefined ? status : fleetStatus
  const decision = st?.decision?.record
  const modeChip = (decision?.mode || st?.heartbeat?.status || '').toUpperCase()
  const manifest = st?.manifest

  return (
    <div className="ctl-topbar">
      {title && (
        <div className="ctl-topbar-brand">
          <img
            src="/logo_agroprosperis.png"
            alt="Агропросперіс"
            className="ctl-topbar-logo"
          />
          <h1>{title}</h1>
        </div>
      )}

      <label className="ctl-topbar-object">
        <span className="ctl-topbar-label">Об'єкт</span>
        <select value={organizationID} onChange={(e) => onOrganizationChange(e.target.value)}>
          {options.map((id) => (
            <option key={id} value={id}>
              {formatOrganizationLabel(id)} · {id.toUpperCase()}
            </option>
          ))}
        </select>
      </label>

      <div className="ctl-topbar-mode">
        <span className="ctl-topbar-label">Режим</span>
        <div className="ctl-mode-switch" role="tablist" aria-label="Режим інтерфейсу">
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'analytics'}
            className={mode === 'analytics' ? 'active' : ''}
            onClick={() => mode !== 'analytics' && navigateView('dashboard')}
          >
            Аналітика та моніторинг
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'control'}
            className={mode === 'control' ? 'active' : ''}
            onClick={() => mode !== 'control' && navigateView('control')}
          >
            Керування
          </button>
        </div>
      </div>

      <div className="ctl-topbar-chips">
        {st && (
          <span className={'ctl-chip ' + (st.heartbeat.online ? 'ok' : 'err')}>
            <span className="ctl-dot" />
            IOT2050 {st.heartbeat.online ? 'online' : 'offline'}
          </span>
        )}
        {modeChip && <span className="ctl-chip plain">{modeChip}</span>}
        {mode === 'control' && manifest?.manifest_id && (
          <span
            className={
              'ctl-chip ' +
              (manifest.state === 'applied' ? 'ok' : manifest.state === 'expired' ? 'err' : 'warn')
            }
            title={manifest.manifest_id}
          >
            Manifest {shortManifestID(manifest.manifest_id)}{' '}
            {manifest.state === 'applied'
              ? 'applied'
              : manifest.state === 'expired'
                ? 'прострочений'
                : 'очікує'}
          </span>
        )}
      </div>
    </div>
  )
}
