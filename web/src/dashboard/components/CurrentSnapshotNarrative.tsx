import type { RegisterMeta } from '../../types'
import type { LiveAllocation } from '../transforms/liveAllocation'
import { EnergyFlowLive } from './EnergyFlowLive'

// CurrentSnapshotNarrative renders the "Поточне енергоспоживання"
// card on the left panel. Visually it embeds a compact copy of
// EnergyFlowLive — the same four-corner / central-hub diagram with
// animated bezier paths — so operators see live power flow at a
// glance without bouncing to the standalone diagram. The compact
// layout is driven entirely by EnergyFlowLive's `variant="compact"`
// prop; this component only owns the section heading and the
// busy/stale ARIA wiring.

type Props = {
  liveAllocation: LiveAllocation
  loading: boolean
  debug: boolean
  registers: Record<string, RegisterMeta> | null
}

export function CurrentSnapshotNarrative({
  liveAllocation,
  loading,
  debug,
  registers,
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
