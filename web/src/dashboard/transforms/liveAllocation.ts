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
// Site load is derived from the bus-balance identity instead of
// being read from the SmartLogger's `load_power_kw` register
// (Modbus 40503): that register tracks only the inverter's
// "Backup load" branch and is unreliable under normal grid-tied
// operation — in some modes it overstates load (e.g. reads 86 kW
// while the true site load is 12 kW), in others it undercounts.
// Mirrors the day-chart derivation in `transforms/power.ts` and
// the cards card derivation in `CurrentSnapshotNarrative.tsx`:
//   site_load = |pv + grid + ess|   (with ESS in normalized
//                                    "positive = discharge" sign)
// Raw `load_power_kw` is consulted only as a fallback when one of
// the three bus inputs is missing entirely.
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
// `unavailable` = SmartLogger returned its INT32/UINT32 "all-ones"
// sentinel (typically when the grid meter is offline / the site is
// islanded). The UI renders a dash instead of a kW number.
export type LiveGridState = 'importing' | 'exporting' | 'idle' | 'unavailable'

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
  // central status hub. Zero when the grid reading is unavailable.
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

// INT32_MAX / UINT32_MAX scaled by the Huawei power-register gain
// (0.001). SmartLogger writes these sentinels when a meter is
// unreachable — most commonly the grid POC meter during islanding
// or a Modbus outage. 2_147_483.647 kW is not a physical reading;
// treating it as one blew the live diagram up to "2.1 GW import".
const INT32_MAX = 2147483647
const UINT32_MAX = 4294967295
const POWER_GAIN = 0.001

// isModbusPowerSentinel reports whether a kW value is one of the
// well-known SmartLogger invalid sentinels after gain scaling.
// Tolerance of `gain * 10` matches energyflow.IsInvalidUint32Scaled
// so float64 round-trip noise doesn't miss the hit.
export function isModbusPowerSentinel(value: number): boolean {
  if (!Number.isFinite(value)) return false
  const tol = POWER_GAIN * 10
  if (Math.abs(value - INT32_MAX * POWER_GAIN) < tol) return true
  if (Math.abs(value - INT32_MIN * POWER_GAIN) < tol) return true
  if (Math.abs(value - UINT32_MAX * POWER_GAIN) < tol) return true
  return false
}

// INT32_MIN as a Number literal (safe within float64 mantissa for
// this magnitude). Used only for the negative signed sentinel.
const INT32_MIN = -2147483648

function readNumber(metric: CurrentMetric | undefined): number {
  if (!metric) return 0
  const n = Number(metric.value)
  if (!Number.isFinite(n) || isModbusPowerSentinel(n)) return 0
  return n
}

function readOptionalNumber(metric: CurrentMetric | undefined): number | null {
  if (!metric) return null
  const n = Number(metric.value)
  if (!Number.isFinite(n) || isModbusPowerSentinel(n)) return null
  return n
}

// derivedLoadKw computes site-wide load from the bus-balance
// identity. Returns null when PV or ESS is missing — a partial
// sum would mislead, in which case the caller falls back to the
// raw `load_power_kw` register reading.
//
// A missing/sentinel *grid* reading is treated as 0 kW (islanded /
// meter offline). That matches Kirchhoff when there is no grid
// exchange and avoids poisoning load with ~2.1e6 kW from the
// INT32_MAX sentinel. `Math.abs` matches
// `CurrentSnapshotNarrative.derivedLoadKw` and handles the rare
// case where rounding leaves the sum slightly negative.
function derivedLoadKw(
  metrics: CurrentResponse['metrics'],
  essDischargeSign: 1 | -1,
): number | null {
  const pv = readOptionalNumber(metrics.active_pv_power_kw)
  const ess = readOptionalNumber(metrics.active_ess_power_kw)
  if (pv === null || ess === null) return null
  // Grid may be null (sentinel / missing) — treat as 0 for the
  // islanded bus balance. Explicit zero from a healthy meter is
  // already a real reading and arrives as 0 via readOptionalNumber.
  const grid = readOptionalNumber(metrics.grid_connected_active_power_kw) ?? 0
  return Math.abs(pv + grid + essDischargeSign * ess)
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
  // Grid meter offline / islanded: SmartLogger returns INT32_MAX
  // (→ ~2.15e6 kW after gain). Detect before any bus math so we
  // don't invent a multi-GW import and poison derived load.
  const rawGrid = m.grid_connected_active_power_kw
    ? Number(m.grid_connected_active_power_kw.value)
    : null
  const gridUnavailable =
    rawGrid !== null &&
    Number.isFinite(rawGrid) &&
    isModbusPowerSentinel(rawGrid)
  const gridKw = gridUnavailable ? 0 : readNumber(m.grid_connected_active_power_kw)
  const essKw = essDischargeSign * readNumber(m.active_ess_power_kw)
  // Prefer bus-balance derivation; raw 40503 is a backup-load-only
  // reading that misrepresents site-wide consumption in many modes.
  const loadKw =
    derivedLoadKw(m, essDischargeSign) ?? readNumber(m.load_power_kw)
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
  // every flow is genuinely 0 kW. A lone grid sentinel does NOT
  // count as a real reading — without it the site still has PV/ESS.
  const anyReading =
    m.active_pv_power_kw ||
    m.load_power_kw ||
    m.active_ess_power_kw ||
    (m.grid_connected_active_power_kw && !gridUnavailable)
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
    gridState: gridUnavailable
      ? 'unavailable'
      : gridKw > IDLE_KW
        ? 'importing'
        : gridKw < -IDLE_KW
          ? 'exporting'
          : 'idle',
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
