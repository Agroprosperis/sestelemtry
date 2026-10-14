import { Bug } from '@phosphor-icons/react'
import type { RangePreset } from '../range'
import { PeriodPicker } from './PeriodPicker'

type Props = {
  title: string
  titleId?: string
  preset: RangePreset
  anchor: Date
  onAnchorChange: (next: Date) => void
  debug?: boolean
  // Without a handler the debug toggle is not offered.
  onDebugToggle?: () => void
}

// DashboardControls heads one period block of the Система page: the
// block's name on the left, its period picker (and, on the day block,
// the debug toggle) on the right. The object picker lives once, in the
// mode top bar — it switches the whole page, not just one block.
export function DashboardControls({
  title,
  titleId,
  preset,
  anchor,
  onAnchorChange,
  debug = false,
  onDebugToggle,
}: Props) {
  return (
    <div className="dashboard-controls">
      <h2 id={titleId} className="dashboard-period-title">
        {title}
      </h2>
      <PeriodPicker preset={preset} anchor={anchor} onChange={onAnchorChange} />
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
