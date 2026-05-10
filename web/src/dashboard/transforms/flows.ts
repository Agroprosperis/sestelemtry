// flowsFromTotals derives the seven directional flows of the energy
// Sankey diagram from the cumulative-counter totals returned by
// /api/v1/energy-summary. It mirrors the spec's allocation rule but
// runs on period totals rather than per-second deltas: by the time
// the totals reach the dashboard, the collector's energyflow
// aggregator has already summed every 60 s window, so the four
// `*_to_*_kwh` synthetic counters are authoritative for ESS
// directionality. The remaining three flows (PV→Load, PV→Grid,
// Grid→Load) are derived algebraically from the SmartLogger
// accumulators.
//
// Mirrors the backend `Allocate` function in
// internal/energyflow/allocate.go but operates on already-summed
// per-period counters; the four `pv/grid_to_ess` and `ess_to_load/grid`
// values come straight from the collector's aggregator output, so
// this transform is just a fan-out + algebra step. Negative or
// missing inputs collapse to zero so a brand-new deployment (no
// energyflow samples yet) renders a degraded but valid diagram
// instead of a blank card.

export type EnergyFlows = {
  pvToLoadKwh: number
  pvToEssKwh: number
  pvToGridKwh: number
  gridToLoadKwh: number
  gridToEssKwh: number
  essToLoadKwh: number
  essToGridKwh: number

  // Aggregate node throughputs, useful when sizing Sankey node
  // rectangles. Each node's IN total equals its OUT total within
  // float rounding because flowsFromTotals constructs the seven
  // edges from the same counters.
  pvProducedKwh: number
  gridImportKwh: number
  gridExportKwh: number
  essChargedKwh: number
  essDischargedKwh: number
  loadConsumedKwh: number

  // hasEnergyFlowSamples reports whether the four synthetic
  // counters arrived (i.e. the collector's energyflow aggregator
  // has produced at least one sample in the period). When false we
  // still show the diagram, but the ESS edges will be zero — the
  // card surfaces a hint so operators know what to fix.
  hasEnergyFlowSamples: boolean
}

const SYNTHETIC_KEYS = [
  'pv_to_ess_kwh',
  'grid_to_ess_kwh',
  'ess_to_load_kwh',
  'ess_to_grid_kwh',
] as const

function nonNegative(value: number | undefined): number {
  return Number.isFinite(value) && (value as number) > 0 ? (value as number) : 0
}

export function flowsFromTotals(totals: Record<string, number>): EnergyFlows {
  const pvProduced = nonNegative(totals.accumulated_pv_energy_yield_kwh)
  const gridImport = nonNegative(totals.accumulated_electricity_purchased_kwh)
  const gridExport = nonNegative(totals.accumulated_electricity_sold_kwh)
  const essCharged = nonNegative(totals.total_energy_charged_kwh)
  const essDischarged = nonNegative(totals.total_energy_discharged_kwh)

  const pvToEss = nonNegative(totals.pv_to_ess_kwh)
  const gridToEss = nonNegative(totals.grid_to_ess_kwh)
  const essToLoad = nonNegative(totals.ess_to_load_kwh)
  const essToGrid = nonNegative(totals.ess_to_grid_kwh)

  // Cap synthetic ESS-side flows at the measured charge/discharge
  // counters so a partial seed (e.g. only `ess_to_load_kwh` is
  // present) cannot inflate the diagram beyond the underlying
  // physical metric.
  const pvToEssCapped = Math.min(pvToEss, essCharged)
  const gridToEssCapped = Math.min(gridToEss, Math.max(essCharged - pvToEssCapped, 0))
  const essToLoadCapped = Math.min(essToLoad, essDischarged)
  const essToGridCapped = Math.min(essToGrid, Math.max(essDischarged - essToLoadCapped, 0))

  // Algebraic flows (no synthetic counter):
  //   PV → Grid = sold (what physically left the inverter to the
  //               grid; the spec attributes export to PV when ESS
  //               doesn't discharge to grid simultaneously).
  //   PV → Load = pvProduced - sold - pvToEss (whatever PV produced
  //               and didn't export or store went to the load).
  //   Grid → Load = purchased - gridToEss (whatever was imported
  //                 minus what charged the battery went to the load).
  const pvToGrid = Math.max(gridExport - essToGridCapped, 0)
  const pvToLoad = Math.max(pvProduced - pvToGrid - pvToEssCapped, 0)
  const gridToLoad = Math.max(gridImport - gridToEssCapped, 0)

  const loadConsumed = pvToLoad + gridToLoad + essToLoadCapped

  const hasEnergyFlowSamples = SYNTHETIC_KEYS.some(
    (k) => Number.isFinite(totals[k]) && totals[k] !== 0,
  )

  return {
    pvToLoadKwh: pvToLoad,
    pvToEssKwh: pvToEssCapped,
    pvToGridKwh: pvToGrid,
    gridToLoadKwh: gridToLoad,
    gridToEssKwh: gridToEssCapped,
    essToLoadKwh: essToLoadCapped,
    essToGridKwh: essToGridCapped,
    pvProducedKwh: pvProduced,
    gridImportKwh: gridImport,
    gridExportKwh: gridExport,
    essChargedKwh: essCharged,
    essDischargedKwh: essDischarged,
    loadConsumedKwh: loadConsumed,
    hasEnergyFlowSamples,
  }
}

// EMPTY_FLOWS is used as the default state before /api/v1/energy-summary
// resolves so the Sankey card can render placeholders without optional
// chaining at every consumer site.
export const EMPTY_FLOWS: EnergyFlows = flowsFromTotals({})
