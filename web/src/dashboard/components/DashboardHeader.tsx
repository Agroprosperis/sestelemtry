import { Bug } from '@phosphor-icons/react'
import type { RangePreset } from '../range'
import { OrganizationSelect } from './OrganizationSelect'
import { PeriodPicker } from './PeriodPicker'
import { RangeSwitch } from './RangeSwitch'

// goToEconomicsView rewrites the URL to ?view=economics while
// preserving the current organization_id. Implemented here (vs in
// `Dashboard.tsx`) so the header's switch button can stay a
// pure presentational component without prop-drilling a router.
function goToEconomicsView() {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  url.searchParams.set('view', 'economics')
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

export function DashboardHeader({
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
    <header className="dashboard-header">
      <div className="dashboard-header-brand">
        <img
          src="/logo_agroprosperis.png"
          alt="Агропросперіс"
          className="dashboard-header-logo"
        />
        <div>
          <h1>Моніторинг СЕС + УЗЕ</h1>
          <p>Організація: {organizationID}</p>
        </div>
      </div>
      <div className="header-controls">
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
    </header>
  )
}
