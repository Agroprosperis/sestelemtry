import { Bug } from '@phosphor-icons/react'
import type { RangePreset } from '../range'
import { PeriodPicker } from './PeriodPicker'
import { RangeSwitch } from './RangeSwitch'

type Props = {
  preset: RangePreset
  onPresetChange: (next: RangePreset) => void
  // presetLocked hides the Day/Month/Year switch for day-chart roles.
  presetLocked?: boolean
  anchor: Date
  onAnchorChange: (next: Date) => void
  debug: boolean
  // Without a handler the debug toggle is not offered.
  onDebugToggle?: () => void
}

// DashboardControls is the full-width strip under the mode top bar:
// range / period drive the whole page.
// The object (organization) picker lives once, in the mode top bar —
// it switches the whole page, not just this pane.
export function DashboardControls({
  preset,
  onPresetChange,
  presetLocked = false,
  anchor,
  onAnchorChange,
  debug,
  onDebugToggle,
}: Props) {
  return (
    <div className="dashboard-controls">
      {!presetLocked && <RangeSwitch value={preset} onChange={onPresetChange} />}
      {/* Without the switch the picker is the strip's first item and sits
          at its left edge, so «Сьогодні» goes after it. */}
      <PeriodPicker
        preset={preset}
        anchor={anchor}
        onChange={onAnchorChange}
        todaySide={presetLocked ? 'end' : 'start'}
      />
      {onDebugToggle && (
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
      )}
    </div>
  )
}
