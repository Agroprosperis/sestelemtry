import { describe, expect, it } from 'vitest'
import { dayQualityTip } from '../monthly/rollup'

// dayQualityTip renders the operator sentence behind the amber "!" mark
// in the daily / monthly detail tables. The flag strings come verbatim
// from the backend's reconciliation diagnostics.
describe('dayQualityTip', () => {
  it('translates counter-issue flags into one sentence', () => {
    const tip = dayQualityTip(['import_lag:512', 'load_mismatch:0.5930'])
    expect(tip).toContain('512')
    expect(tip).toContain('59%')
    expect(tip).toContain('приблизні')
  })

  it('ignores routine bookkeeping flags', () => {
    expect(dayQualityTip(['no_scale:grid_export', 'load_rebalanced'])).toBeNull()
    expect(dayQualityTip([])).toBeNull()
    expect(dayQualityTip(undefined)).toBeNull()
  })
})
