// liveAllocation transforms a single /api/v1/current snapshot into
// the seven directional kW edges that the live energy-flow diagram
// renders. It mirrors the backend `Allocate` rule (internal/energyflow/
// allocate.go) but operates on instantaneous power readings instead
// of accumulator deltas — this is the diagram's job: show, right
// now, where each kilowatt is going.
//
// Sign convention (matches Huawei SmartLogger firmware on PE/ZE,
// confirmed against live readings; mirrored in PowerTooltip.tsx):
//   * active_pv_power_kw         > 0 → PV is generating
//   * load_power_kw              > 0 → site is consuming
//   * grid_connected_active_power_kw > 0 → site is importing from grid;
//                                       < 0 → exporting
//   * active_ess_power_kw        > 0 → battery discharging;
//                                       < 0 → charging
//
// `essDischargeSign` flips the ESS power reading for organizations
// whose firmware reports the opposite sign. The default is 1; when
// the org config sets ess_discharge_sign: -1 we flip the reading
// so "positive = discharge" still holds downstream.
//
// Edges are clamped at zero — the algorithm fans inputs out to
// outputs in priority order (load first, ESS second, grid third)
// and never produces a negative flow even if the source kW barely
// exceeds the destination's draw within sensor noise.

import type { CurrentResponse, CurrentMetric } from '../../types'

// Anything closer to zero than this is treated as "idle". Same
// threshold as PowerTooltip — the ESS and grid meters routinely
// sit at ±tens of watts on inverter standby, and labelling those
// as "charging" / "importing" makes the live diagram flicker on
// every poll. 50 W is small enough not to hide genuine activity.
export const IDLE_KW = 0.05

export type LivePvState = 'generating' | 'idle'
export type LiveLoadState = 'consuming' | 'idle'
export type LiveEssState = 'charging' | 'discharging' | 'idle'
export type LiveGridState = 'importing' | 'exporting' | 'idle'

export type LiveAllocation = {
  pvKw: number
  pvState: LivePvState
  loadKw: number
  loadState: LiveLoadState
  essKw: number
  essState: LiveEssState
  socPercent: number | null
  gridKw: number
  gridState: LiveGridState
  pvToLoadKw: number
  pvToEssKw: number
  pvToGridKw: number
  gridToLoadKw: number
  gridToEssKw: number
  essToLoadKw: number
  essToGridKw: number
  // netExportKw: positive when the site exports more than it
  // imports (= grid_export - grid_import). Drives the "Exporting
  // X kW to grid" / "Importing Y kW from grid" subtitle in the
  // central status hub.
  netExportKw: number
  status: 'normal' | 'no_data'
  // observedAt mirrors the freshest timestamp we found among the
  // four power readings. The dashboard surfaces it as "Updated N s
  // ago" so operators can spot stale snapshots at a glance.
  observedAt: Date | null
}

export const NO_DATA_ALLOCATION: LiveAllocation = {
  pvKw: 0,
  pvState: 'idle',
  loadKw: 0,
  loadState: 'idle',
  essKw: 0,
  essState: 'idle',
  socPercent: null,
  gridKw: 0,
  gridState: 'idle',
  pvToLoadKw: 0,
  pvToEssKw: 0,
  pvToGridKw: 0,
  gridToLoadKw: 0,
  gridToEssKw: 0,
  essToLoadKw: 0,
  essToGridKw: 0,
  netExportKw: 0,
  status: 'no_data',
  observedAt: null,
}

function readNumber(metric: CurrentMetric | undefined): number {
  if (!metric) return 0
  const n = Number(metric.value)
  return Number.isFinite(n) ? n : 0
}

function readOptionalNumber(metric: CurrentMetric | undefined): number | null {
  if (!metric) return null
  const n = Number(metric.value)
  return Number.isFinite(n) ? n : null
}

function freshestTimestamp(metrics: Iterable<CurrentMetric | undefined>): Date | null {
  let latest: number | null = null
  for (const m of metrics) {
    if (!m?.time) continue
    const t = Date.parse(m.time)
    if (!Number.isFinite(t)) continue
    if (latest === null || t > latest) latest = t
  }
  return latest === null ? null : new Date(latest)
}

export function liveAllocationFromCurrent(
  current: CurrentResponse | null,
  essDischargeSign: 1 | -1 = 1,
): LiveAllocation {
  if (!current) return NO_DATA_ALLOCATION

  const m = current.metrics
  const pvKw = readNumber(m.active_pv_power_kw)
  const loadKw = readNumber(m.load_power_kw)
  const gridKw = readNumber(m.grid_connected_active_power_kw)
  const essKw = essDischargeSign * readNumber(m.active_ess_power_kw)
  const socPercent = readOptionalNumber(m.soc_percent)

  const observedAt = freshestTimestamp([
    m.active_pv_power_kw,
    m.load_power_kw,
    m.grid_connected_active_power_kw,
    m.active_ess_power_kw,
  ])

  // No power readings at all = the device is offline / hasn't
  // reported yet. We still return a valid allocation but flag it
  // so the UI can render a "no data" pill instead of pretending
  // every flow is genuinely 0 kW.
  const anyReading =
    m.active_pv_power_kw ||
    m.load_power_kw ||
    m.grid_connected_active_power_kw ||
    m.active_ess_power_kw
  if (!anyReading) {
    return { ...NO_DATA_ALLOCATION, observedAt }
  }

  const pvProd = Math.max(pvKw, 0)
  const gridImport = Math.max(gridKw, 0)
  const gridExport = Math.max(-gridKw, 0)
  const essDischarge = Math.max(essKw, 0)
  const essCharge = Math.max(-essKw, 0)

  // PV-side allocation: load first, then ESS charging, then export.
  // Mirrors backend `Allocate`: the spec says PV production goes to
  // load (highest priority), surplus charges the battery, the rest
  // is exported. We never produce a negative flow.
  let loadRemaining = Math.max(loadKw, 0)
  const pvToLoad = Math.min(pvProd, loadRemaining)
  loadRemaining -= pvToLoad
  let pvSurplus = pvProd - pvToLoad
  const pvToEss = Math.min(pvSurplus, essCharge)
  pvSurplus -= pvToEss
  const essChargeFromGrid = Math.max(essCharge - pvToEss, 0)
  const pvToGrid = Math.max(pvSurplus, 0)

  // ESS-discharge → load → grid (any leftover discharge that the
  // load can't absorb shows up at the export point).
  const essToLoad = Math.min(essDischarge, loadRemaining)
  loadRemaining -= essToLoad
  const essToGrid = Math.max(essDischarge - essToLoad, 0)

  // Grid-import → load → ESS. We charge the battery from the grid
  // only when PV alone couldn't cover the requested charge (the
  // backend uses the same priority order).
  const gridToLoad = Math.min(gridImport, loadRemaining)
  const gridToEss = Math.min(Math.max(gridImport - gridToLoad, 0), essChargeFromGrid)

  const netExportKw = gridExport - gridImport

  return {
    pvKw: pvProd,
    pvState: pvProd > IDLE_KW ? 'generating' : 'idle',
    loadKw: Math.max(loadKw, 0),
    loadState: loadKw > IDLE_KW ? 'consuming' : 'idle',
    essKw: Math.abs(essKw),
    essState:
      essKw > IDLE_KW ? 'discharging' : essKw < -IDLE_KW ? 'charging' : 'idle',
    socPercent,
    gridKw: Math.abs(gridKw),
    gridState:
      gridKw > IDLE_KW ? 'importing' : gridKw < -IDLE_KW ? 'exporting' : 'idle',
    pvToLoadKw: pvToLoad,
    pvToEssKw: pvToEss,
    pvToGridKw: pvToGrid,
    gridToLoadKw: gridToLoad,
    gridToEssKw: gridToEss,
    essToLoadKw: essToLoad,
    essToGridKw: essToGrid,
    netExportKw,
    status: 'normal',
    observedAt,
  }
}
