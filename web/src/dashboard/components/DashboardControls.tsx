import { Bug } from '@phosphor-icons/react'
import type { RangePreset } from '../range'
import { OrganizationSelect } from './OrganizationSelect'
import { PeriodPicker } from './PeriodPicker'
import { RangeSwitch } from './RangeSwitch'

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

// goToImportView rewrites the URL to ?view=import (preserving the
// current organization_id) for the FusionSolar archive backfill page.
function goToImportView() {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  url.searchParams.set('view', 'import')
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
}: Props) {
  return (
    <div className="dashboard-controls">
      <OrganizationSelect value={organizationID} options={organizationOptions} onChange={onOrganizationChange} />
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
      <button
        type="button"
        className="economics-switch-button"
        onClick={goToImportView}
        title="Імпорт архівних даних із FusionSolar"
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
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
          <path d="M7 10l5 5 5-5" />
          <path d="M12 15V3" />
        </svg>
        <span>Імпорт архіву</span>
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
