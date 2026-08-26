import type { RegisterMeta } from '../../types'
import type { LiveAllocation } from '../transforms/liveAllocation'
import { EnergyFlowLive } from './EnergyFlowLive'
import { MetricsAtPicker } from './MetricsAtPicker'

// CurrentSnapshotNarrative renders the "Поточне енергоспоживання"
// card on the left panel. Visually it embeds a compact copy of
// EnergyFlowLive — the same four-corner / central-hub diagram with
// animated bezier paths — so operators see live power flow at a
// glance without bouncing to the standalone diagram. The compact
// layout is driven entirely by EnergyFlowLive's `variant="compact"`
// prop; this component owns the section heading, the live/at-time
// snapshot picker (when the parent wires it) and the busy/stale
// ARIA wiring.

type Props = {
  liveAllocation: LiveAllocation
  loading: boolean
  debug: boolean
  registers: Record<string, RegisterMeta> | null
  // When onMetricsAtChange is provided the card hosts the
  // «Реальний час | На момент» picker (analytics dashboard);
  // the control «Стан» tab omits it and stays live-only.
  metricsAt?: Date | null
  onMetricsAtChange?: (next: Date | null) => void
}

function formatSnapshotLabel(at: Date): string {
  return at.toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

export function CurrentSnapshotNarrative({
  liveAllocation,
  loading,
  debug,
  registers,
  metricsAt,
  onMetricsAtChange,
}: Props) {
  return (
    <section
      className="metrics-group current-snapshot-card"
      aria-labelledby="current-snapshot-title"
      aria-busy={loading}
    >
      <h2 id="current-snapshot-title" className="metrics-group-title">
        Поточне енергоспоживання
      </h2>
      {onMetricsAtChange && (
        <div className="current-snapshot-at">
          <MetricsAtPicker value={metricsAt ?? null} onChange={onMetricsAtChange} />
          {metricsAt && (
            <p className="metrics-at-hint">
              Показники станом на <strong>{formatSnapshotLabel(metricsAt)}</strong>
            </p>
          )}
        </div>
      )}
      <EnergyFlowLive
        allocation={liveAllocation}
        variant="compact"
        wrapInSection={false}
        debug={debug}
        registers={registers}
      />
    </section>
  )
}
