import type { EdgeSiteStatus } from '../control/controlClient'
import type { ConsoleMode } from './ModeTopBar'

function shortManifestID(id: string): string {
  const parts = id.split('-')
  const site = (parts[0] ?? '').toUpperCase()
  const hash = parts[parts.length - 1] ?? ''
  return `${site}-${hash.slice(0, 4)}`
}

// EdgeStatusChips is the IOT2050 / mode / health / manifest cluster.
// On «Керування» it sits in the tab row; elsewhere it stays in the top bar.
export function EdgeStatusChips({
  status,
  mode,
}: {
  status: EdgeSiteStatus | null
  mode: ConsoleMode
}) {
  const decision = status?.decision?.record
  const modeChip = (decision?.mode || status?.heartbeat?.status || '').toUpperCase()
  const manifest = status?.manifest
  if (!status && !modeChip) return null

  return (
    <div className="ctl-topbar-chips">
      {status && (
        <span className={'ctl-chip ' + (status.heartbeat.online ? 'ok' : 'err')}>
          <span className="ctl-dot" />
          IOT2050 {status.heartbeat.online ? 'online' : 'offline'}
        </span>
      )}
      {modeChip && <span className="ctl-chip plain">{modeChip}</span>}
      {mode === 'control' && status?.health && (
        <span
          className={'ctl-chip ' + (status.health.ok ? 'ok' : 'warn')}
          title={
            status.health.ok
              ? 'Усі перевірки health пройдено'
              : (status.health.checks ?? [])
                  .filter((c) => !c.ok && (c.severity === 'warning' || c.severity === 'alarm'))
                  .map((c) => c.label || c.id)
                  .join(', ') || 'Є відхилення'
          }
        >
          {status.health.ok ? 'діагностика OK' : 'є відхилення'}
        </span>
      )}
      {mode === 'control' && manifest?.manifest_id && (
        <span
          className={
            'ctl-chip ' +
            (manifest.state === 'applied' ? 'ok' : manifest.state === 'expired' ? 'err' : 'warn')
          }
          title={`Manifest ${manifest.manifest_id}`}
        >
          {shortManifestID(manifest.manifest_id)}{' '}
          {manifest.state === 'applied' ? 'applied' : manifest.state === 'expired' ? 'прострочений' : 'очікує'}
        </span>
      )}
    </div>
  )
}
