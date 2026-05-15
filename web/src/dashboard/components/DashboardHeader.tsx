import { formatOrganizationLabel } from '../config'

type Props = {
  organizationID: string
}

// DashboardHeader is the page-wide brand strip: logo on the left,
// product name + organization subtitle next to it. The interactive
// controls (org switcher, range buttons, period picker, debug
// toggle, etc.) intentionally live in a separate `DashboardControls`
// strip rendered above the right-hand charts column — see
// `Dashboard.tsx` — so the brand bar stays uncluttered and the
// controls sit visually next to the data they affect.
export function DashboardHeader({ organizationID }: Props) {
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
    </header>
  )
}
