import { formatOrganizationLabel } from '../config'

type Props = {
  organizationID: string
  onExportClick?: () => void
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

// DashboardHeader is the page-wide brand strip: logo + product name on
// the left, and the global actions (archive import, data export) pinned
// to the right. Range/period/org controls live in `DashboardControls`
// next to the charts they affect — see `Dashboard.tsx`.
export function DashboardHeader({ organizationID, onExportClick }: Props) {
  return (
    <header className="dashboard-header">
      <div className="dashboard-header-brand">
        <img
          src="/logo_agroprosperis.png"
          alt="Агропросперіс"
          className="dashboard-header-logo"
        />
        <div className="dashboard-header-titles">
          <h1>Моніторинг СЕС + УЗЕ</h1>
          <p>{formatOrganizationLabel(organizationID)}</p>
        </div>
      </div>

      <div className="dashboard-header-actions">
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
    </header>
  )
}
