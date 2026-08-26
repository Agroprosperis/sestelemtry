// ModeTopBar is the shared strip above both the analytics dashboard and
// the «Керування» view: object picker + mode switch + edge status chips
// (IOT2050 online, active mode, manifest delivery state).

import { useEffect, useState } from 'react'
import { formatOrganizationLabel } from '../dashboard/config'
import { fetchEdgeFleet, type EdgeSiteStatus } from './controlClient'
import './control.css'

export type ConsoleMode = 'analytics' | 'control'

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
  // status lets the control page share its own poll; when omitted
  // (analytics) the bar quietly polls the fleet itself.
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

export function ModeTopBar({ mode, organizationID, options, onOrganizationChange, status }: Props) {
  const [fleetStatus, setFleetStatus] = useState<EdgeSiteStatus | null>(null)

  // Analytics mode has no other edge data source — poll the fleet to
  // know whether the selected object has an edge device at all.
  useEffect(() => {
    if (status !== undefined) return
    let cancelled = false
    const load = () =>
      fetchEdgeFleet()
        .then((f) => {
          if (cancelled) return
          setFleetStatus(f.sites.find((s) => s.site_id === organizationID) ?? null)
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
