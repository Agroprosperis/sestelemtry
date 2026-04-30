import type { RangePreset } from '../range'
import { OrganizationSelect } from './OrganizationSelect'
import { PeriodPicker } from './PeriodPicker'
import { RangeSwitch } from './RangeSwitch'

type Props = {
  organizationID: string
  organizationOptions: string[]
  onOrganizationChange: (next: string) => void
  preset: RangePreset
  onPresetChange: (next: RangePreset) => void
  anchor: Date
  onAnchorChange: (next: Date) => void
}

export function DashboardHeader({
  organizationID,
  organizationOptions,
  onOrganizationChange,
  preset,
  onPresetChange,
  anchor,
  onAnchorChange,
}: Props) {
  return (
    <header className="dashboard-header">
      <div>
        <h1>Telemetry Dashboard</h1>
        <p>Organization: {organizationID}</p>
      </div>
      <div className="header-controls">
        <OrganizationSelect value={organizationID} options={organizationOptions} onChange={onOrganizationChange} />
        <RangeSwitch value={preset} onChange={onPresetChange} />
        <PeriodPicker preset={preset} anchor={anchor} onChange={onAnchorChange} />
      </div>
    </header>
  )
}
