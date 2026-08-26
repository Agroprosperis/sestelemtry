import { Bug } from '@phosphor-icons/react'
import type { RangePreset } from '../range'
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
    </div>
  )
}
