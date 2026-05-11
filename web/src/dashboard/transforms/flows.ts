// flowsFromTotals derives the seven directional flows of the energy
// diagram from the raw cumulative-counter totals and the on-the-fly
// flow totals returned by /api/v1/energy-summary.
//
// The four ESS-side flows (PV→ESS, Grid→ESS, ESS→Load, ESS→Grid)
// come straight from the API's `flows` field — the server has
// already run energyflow.Recompute on the raw Modbus samples for the
// requested window. The remaining three flows (PV→Load, PV→Grid,
// Grid→Load) are derived algebraically from the SmartLogger
// accumulators in `totals`.
//
// `flows == null` means the API did not run the allocator (either
// the caller didn't ask for it or the window was wider than the
// on-the-fly compute budget). In that case the ESS-side edges
// collapse to zero and the dashboard's empty-state copy explains the
// situation. Treating null and an all-zero pointer differently is
// deliberate: a successful allocator run that happened to return
// zeros (e.g. a flat period with no battery activity) is real data,
// not "we didn't try".

import type { EnergyFlowTotals } from '../../api'

export type EnergyFlows = {
  pvToLoadKwh: number
  pvToEssKwh: number
  pvToGridKwh: number
  gridToLoadKwh: number
  gridToEssKwh: number
  essToLoadKwh: number
  essToGridKwh: number

  // Aggregate node throughputs, useful when sizing diagram node
  // rectangles. Each node's IN total equals its OUT total within
  // float rounding because flowsFromTotals constructs the seven
  // edges from the same counters.
  pvProducedKwh: number
  gridImportKwh: number
  gridExportKwh: number
  essChargedKwh: number
  essDischargedKwh: number
  loadConsumedKwh: number
}

function nonNegative(value: number | undefined | null): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
    ? value
    : 0
}

export function flowsFromTotals(
  totals: Record<string, number>,
  flows: EnergyFlowTotals | null | undefined,
): EnergyFlows {
  const pvProduced = nonNegative(totals.accumulated_pv_energy_yield_kwh)
  const gridImport = nonNegative(totals.accumulated_electricity_purchased_kwh)
  const gridExport = nonNegative(totals.accumulated_electricity_sold_kwh)
  const essCharged = nonNegative(totals.total_energy_charged_kwh)
  const essDischarged = nonNegative(totals.total_energy_discharged_kwh)

  const pvToEss = nonNegative(flows?.pv_to_ess_kwh)
  const gridToEss = nonNegative(flows?.grid_to_ess_kwh)
  const essToLoad = nonNegative(flows?.ess_to_load_kwh)
  const essToGrid = nonNegative(flows?.ess_to_grid_kwh)

  // Algebraic flows (no synthetic counter):
  //   PV → Grid = sold (what physically left the inverter to the
  //               grid; the spec attributes export to PV when ESS
  //               doesn't discharge to grid simultaneously).
  //   PV → Load = pvProduced - sold - pvToEss (whatever PV produced
  //               and didn't export or store went to the load).
  //   Grid → Load = purchased - gridToEss (whatever was imported
  //                 minus what charged the battery went to the load).
  const pvToGrid = Math.max(gridExport - essToGrid, 0)
  const pvToLoad = Math.max(pvProduced - pvToGrid - pvToEss, 0)
  const gridToLoad = Math.max(gridImport - gridToEss, 0)
  const loadConsumed = pvToLoad + gridToLoad + essToLoad

  return {
    pvToLoadKwh: pvToLoad,
    pvToEssKwh: pvToEss,
    pvToGridKwh: pvToGrid,
    gridToLoadKwh: gridToLoad,
    gridToEssKwh: gridToEss,
    essToLoadKwh: essToLoad,
    essToGridKwh: essToGrid,
    pvProducedKwh: pvProduced,
    gridImportKwh: gridImport,
    gridExportKwh: gridExport,
    essChargedKwh: essCharged,
    essDischargedKwh: essDischarged,
    loadConsumedKwh: loadConsumed,
  }
}

// EMPTY_FLOWS is used as the default state before /api/v1/energy-summary
// resolves so the diagram card can render placeholders without
// optional chaining at every consumer site.
export const EMPTY_FLOWS: EnergyFlows = flowsFromTotals({}, null)
