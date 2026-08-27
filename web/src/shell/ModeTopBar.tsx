// ModeTopBar is the app-wide strip above the analytics dashboard, the
// «Керування» view and the service pages (station passport, import):
// the single object picker + mode switch + edge status chips (IOT2050
// online, active mode, manifest delivery state).

import { useEffect, useRef, useState } from 'react'
import { fetchEdgeFleet, type EdgeSiteStatus } from '../control/controlClient'
import { formatOrganizationLabel } from '../dashboard/config'
import './shell.css'

// 'none' is for the service pages (station, import): no mode is
// highlighted, every button navigates.
export type ConsoleMode = 'analytics' | 'economics' | 'control' | 'none'

// navigateView switches between the modes without a full reload,
// preserving the rest of the query string (organization_id etc).
export function navigateView(view: 'dashboard' | 'economics' | 'control', tab?: string) {
  const url = new URL(window.location.href)
  if (view === 'dashboard') url.searchParams.delete('view')
  else url.searchParams.set('view', view)
  if (tab) url.searchParams.set('tab', tab)
  else url.searchParams.delete('tab')
  window.history.pushState({}, '', url)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

// TopBarMenuItem is an entry of the «Сервіс» dropdown — rare admin
// actions (imports, exports, passport, alert settings) that don't
// deserve a permanent button row.
export type TopBarMenuItem = {
  id: string
  label: string
  onSelect: () => void
}

type Props = {
  mode: ConsoleMode
  organizationID: string
  options: string[]
  onOrganizationChange: (next: string) => void
  // title renders a page name next to the logo. The mode pages skip it
  // (the active mode button already names the section) so the bar stays
  // one compact line; service pages pass it as their only heading.
  title?: string
  menu?: TopBarMenuItem[]
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
  menu,
  status,
}: Props) {
  const [fleetStatus, setFleetStatus] = useState<EdgeSiteStatus | null>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!menuOpen) return
    const onDown = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMenuOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [menuOpen])

  // Economics is about money, not live hardware — no chips there, and
  // no point polling the fleet for them.
  const showChips = mode !== 'economics'

  // Without a shared poll the bar checks the fleet itself, so any page
  // can show whether the selected object has an edge device online.
  useEffect(() => {
    if (status !== undefined || !showChips) return
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
  }, [status, organizationID, showChips])

  const st = status !== undefined ? status : fleetStatus
  const decision = st?.decision?.record
  const modeChip = (decision?.mode || st?.heartbeat?.status || '').toUpperCase()
  const manifest = st?.manifest

  return (
    // The wrapper is a CSS container: compaction steps in shell.css key
    // on the bar's real width (the page caps at 1400px, so viewport
    // media queries would lie on wide monitors).
    <div className="ctl-topbar-wrap">
    <div className="ctl-topbar">
      <div className="ctl-topbar-brand">
        <img
          src="/logo_agroprosperis.png"
          alt="Агропросперіс"
          className="ctl-topbar-logo"
        />
        {title && <h1>{title}</h1>}
      </div>

      <label className="ctl-topbar-object">
        <span className="ctl-topbar-label">Об'єкт</span>
        <select
          aria-label="Об'єкт"
          value={organizationID}
          onChange={(e) => onOrganizationChange(e.target.value)}
        >
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
            Аналітика
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={mode === 'economics'}
            className={mode === 'economics' ? 'active' : ''}
            onClick={() => mode !== 'economics' && navigateView('economics')}
          >
            Економіка
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

      {showChips && (
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
            title={`Manifest ${manifest.manifest_id}`}
          >
            {shortManifestID(manifest.manifest_id)}{' '}
            {manifest.state === 'applied'
              ? 'applied'
              : manifest.state === 'expired'
                ? 'прострочений'
                : 'очікує'}
          </span>
        )}
      </div>
      )}

      {menu && menu.length > 0 && (
        <div className="ctl-topbar-menu" ref={menuRef}>
          <button
            type="button"
            className="ctl-topbar-menu-btn"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((v) => !v)}
          >
            <svg
              aria-hidden="true"
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <circle cx="12" cy="12" r="3" />
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.01a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
            </svg>
            <span>Сервіс</span>
            <svg
              aria-hidden="true"
              width="10"
              height="10"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <path d="m6 9 6 6 6-6" />
            </svg>
          </button>
          {menuOpen && (
            <div className="ctl-topbar-menu-list" role="menu">
              {menu.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  role="menuitem"
                  onClick={() => {
                    setMenuOpen(false)
                    item.onSelect()
                  }}
                >
                  {item.label}
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
    </div>
  )
}
