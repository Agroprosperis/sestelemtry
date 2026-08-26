import { Bug } from '@phosphor-icons/react'
import type { RangePreset } from '../range'
import { PeriodPicker } from './PeriodPicker'
import { RangeSwitch } from './RangeSwitch'

type Props = {
  preset: RangePreset
  onPresetChange: (next: RangePreset) => void
  anchor: Date
  onAnchorChange: (next: Date) => void
  debug: boolean
  onDebugToggle: () => void
}

// DashboardControls is the strip of interactive widgets that sits
// above the right-hand charts column: range / period drive the charts.
// The object (organization) picker lives once, in the mode top bar —
// it switches the whole page, not just this pane.
export function DashboardControls({
  preset,
  onPresetChange,
  anchor,
  onAnchorChange,
  debug,
  onDebugToggle,
}: Props) {
  return (
    <div className="dashboard-controls">
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
    </div>
  )
}
