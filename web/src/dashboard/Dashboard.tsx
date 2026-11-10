import { lazy, Suspense, useState } from 'react'
import { useAccess } from '../auth/authContext'
import './dashboard.css'
import { ModeTopBar, type TopBarMenuItem } from '../shell/ModeTopBar'
import { useEdgeSiteStatus } from '../shell/useEdgeSiteStatus'
import { navigateView, navigateViewOn } from '../shell/navigation'
import { DashboardControls } from './components/DashboardControls'
import { formatOrganizationLabel, systemSites } from './config'
import { DashboardSite } from './DashboardSite'
import { useDebugMode } from './hooks/useDebugMode'
import { useOrganizationParam } from './hooks/useOrganizationParam'
import { useRangeParams } from './hooks/useRangeParams'
import { useRegistersWhenDebug } from './hooks/useRegistersWhenDebug'

// ExportDialog is also below-the-fold (it only renders when the user
// clicks "Експорт даних"); lazy import keeps its form + custom-export
// fetch helpers out of the initial bundle.
const ExportDialog = lazy(() =>
  import('./components/ExportDialog').then((mod) => ({ default: mod.ExportDialog })),
)

export function Dashboard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam('analytics.day', {
    groups: true,
  })
  // An elevator of several sites shows them one under another: each has
  // its own telemetry, so the page shows each in full instead of a sum.
  const sites = systemSites(organizationID)
  const stacked = sites.length > 1
  const access = useAccess()
  // Engineers get the day chart only; the service tools stay with
  // administrators.
  const fullRange = access.can('analytics.full', organizationID)
  const canService = access.can('service', organizationID)
  const { preset, anchor, setPreset, setAnchor } = useRangeParams({ dayOnly: !fullRange })
  const [metricsAt, setMetricsAt] = useState<Date | null>(null)
  const [exportSite, setExportSite] = useState<string | null>(null)
  const { debug: debugWanted, toggleDebug } = useDebugMode()
  const debug = canService && debugWanted
  const registers = useRegistersWhenDebug(debug)
  const edgeStatus = useEdgeSiteStatus(organizationID, !stacked)

  // The passport and the export are a site's, so an elevator of several
  // offers them once per site.
  const perSite = (id: string, label: string, onSelect: (site: string) => void): TopBarMenuItem[] =>
    sites.map((site) => ({
      id: stacked ? `${id}-${site}` : id,
      label: stacked ? `${label} · ${formatOrganizationLabel(site)}` : label,
      onSelect: () => onSelect(site),
    }))
  const station = perSite('station', 'Паспорт станції', (site) => navigateViewOn('station', site))
  const serviceMenu: TopBarMenuItem[] = canService
    ? [
        ...station,
        { id: 'alerts', label: 'Сповіщення', onSelect: () => navigateView('alerts') },
        { id: 'import', label: 'Імпорт архіву', onSelect: () => navigateView('import') },
        ...perSite('export', 'Експорт даних', setExportSite),
      ]
    : access.can('technical.write', organizationID)
      ? station
      : []

  return (
    <main className="dashboard-page">
      <ModeTopBar
        mode="analytics"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={onOrganizationChange}
        menu={serviceMenu}
      />

      <DashboardControls
        preset={preset}
        onPresetChange={setPreset}
        presetLocked={!fullRange}
        anchor={anchor}
        onAnchorChange={setAnchor}
        debug={debug}
        onDebugToggle={canService ? toggleDebug : undefined}
        edgeStatus={edgeStatus}
      />

      {sites.map((site, i) => (
        <DashboardSite
          key={site}
          organizationID={site}
          preset={preset}
          anchor={anchor}
          metricsAt={metricsAt}
          onMetricsAtChange={setMetricsAt}
          debug={debug}
          registers={registers}
          weather={i === 0}
          titled={stacked}
        />
      ))}

      {exportSite && (
        <Suspense fallback={null}>
          <ExportDialog
            organizationID={exportSite}
            initialAnchor={anchor}
            onClose={() => setExportSite(null)}
          />
        </Suspense>
      )}
    </main>
  )
}
