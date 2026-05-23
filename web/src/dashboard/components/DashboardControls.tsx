import { Bug } from '@phosphor-icons/react'
import type { RangePreset } from '../range'
import { OrganizationSelect } from './OrganizationSelect'
import { PeriodPicker } from './PeriodPicker'
import { RangeSwitch } from './RangeSwitch'
import { ViewSwitch, type ViewMode } from './ViewSwitch'

// goToEconomicsView rewrites the URL to ?view=economics while
// preserving the current organization_id. Implemented here (vs in
// `Dashboard.tsx`) so the strip's switch button can stay a pure
// presentational component without prop-drilling a router.
function goToEconomicsView() {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  url.searchParams.set('view', 'economics')
  window.history.pushState({}, '', url.toString())
  window.dispatchEvent(new PopStateEvent('popstate'))
}

// goToView rewrites the ?view= param to the chosen mode and tells
// the App router to re-read it. Used by the Огляд/Детально switch
// rendered on both the detailed dashboard and the overview page.
function goToView(next: ViewMode) {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  if (next === 'overview') {
    url.searchParams.set('view', 'overview')
  } else {
    url.searchParams.delete('view')
  }
  window.history.pushState({}, '', url.toString())
  window.dispatchEvent(new PopStateEvent('popstate'))
}

type Props = {
  organizationID: string
  organizationOptions: string[]
  onOrganizationChange: (next: string) => void
  preset: RangePreset
  onPresetChange: (next: RangePreset) => void
  anchor: Date
  onAnchorChange: (next: Date) => void
  onExportClick?: () => void
  debug: boolean
  onDebugToggle: () => void
  // view selects which "Огляд / Детально" segment shows as active in
  // the strip. The strip is rendered both in the detailed dashboard
  // (view='dashboard') and on the overview page (view='overview') —
  // the switch updates the URL and triggers App-level routing.
  view?: ViewMode
}

// DashboardControls is the strip of interactive widgets that sits
// above the right-hand charts column. Splitting it out from the
// brand header lets the controls "live next to" the data they
// affect (range / period drive the charts; organization toggles the
// whole pane) while keeping the page-title bar as a clean,
// full-width brand strip.
export function DashboardControls({
  organizationID,
  organizationOptions,
  onOrganizationChange,
  preset,
  onPresetChange,
  anchor,
  onAnchorChange,
  onExportClick,
  debug,
  onDebugToggle,
  view = 'dashboard',
}: Props) {
  return (
    <div className="dashboard-controls">
      <OrganizationSelect value={organizationID} options={organizationOptions} onChange={onOrganizationChange} />
      <ViewSwitch value={view} onChange={goToView} />
      <RangeSwitch value={preset} onChange={onPresetChange} />
      <PeriodPicker preset={preset} anchor={anchor} onChange={onAnchorChange} />
      <button
        type="button"
        className={`debug-toggle${debug ? ' is-active' : ''}`}
        onClick={onDebugToggle}
        aria-pressed={debug}
        title={
          debug
            ? 'Вимкнути режим діагностики (адреси Modbus)'
            : 'Увімкнути режим діагностики (адреси Modbus)'
        }
      >
        <Bug size={14} weight={debug ? 'fill' : 'regular'} />
        <span>Debug</span>
      </button>
      <button
        type="button"
        className="economics-switch-button"
        onClick={goToEconomicsView}
        title="Перейти до сторінки добової економіки (СЕС + УЗЕ)"
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
          <path d="M3 3v18h18" />
          <path d="m7 14 4-4 4 4 5-5" />
        </svg>
        <span>Економіка</span>
      </button>
      {onExportClick && (
        <button
          type="button"
          className="export-data-button"
          onClick={onExportClick}
          title="Експорт даних за довільний період"
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
            <path d="M12 3v12" />
            <path d="m7 10 5 5 5-5" />
            <path d="M5 21h14" />
          </svg>
          <span>Експорт даних</span>
        </button>
      )}
    </div>
  )
}
