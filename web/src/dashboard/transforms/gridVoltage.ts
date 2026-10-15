import type { CurrentResponse } from '../../types'

// GridVoltage is the grid voltage shown on the «Мережа» node: the grid
// meter's three phase voltages when the site reports them, otherwise the
// line voltages the SmartLogger sees through the inverters / PCS.
export type GridVoltage = {
  kind: 'phase' | 'line'
  volts: number[]
  at: Date
}

const PHASE_KEYS = ['meter_phase_a_voltage_v', 'meter_phase_b_voltage_v', 'meter_phase_c_voltage_v']

// Line voltages: the plain keys come from a single-logger read, the
// prefixed ones from the edge controller's per-logger reads. The PV
// inverters read 0 in night standby, so the PCS (УЗЕ) side goes first.
const LINE_GROUPS = [
  ['grid_line_voltage_ab_v', 'grid_line_voltage_bc_v', 'grid_line_voltage_ca_v'],
  ['ess_grid_line_voltage_ab_v', 'ess_grid_line_voltage_bc_v', 'ess_grid_line_voltage_ca_v'],
  ['pv_grid_line_voltage_ab_v', 'pv_grid_line_voltage_bc_v', 'pv_grid_line_voltage_ca_v'],
]

export const GRID_VOLTAGE_KEYS = [...PHASE_KEYS, ...LINE_GROUPS.flat()]

// A reading older than this is a dead link, not the grid's voltage.
export const GRID_VOLTAGE_MAX_AGE_MS = 5 * 60 * 1000

function readGroup(resp: CurrentResponse, keys: string[], now: number): { volts: number[]; at: Date } | null {
  const volts: number[] = []
  let latest = 0
  for (const key of keys) {
    const m = resp.metrics?.[key]
    if (!m || !Number.isFinite(m.value)) return null
    const t = Date.parse(m.time)
    if (!Number.isFinite(t) || now - t > GRID_VOLTAGE_MAX_AGE_MS) return null
    volts.push(m.value)
    latest = Math.max(latest, t)
  }
  // All three at 0 V is a sleeping inverter, not a dead grid.
  if (volts.every((v) => v === 0)) return null
  return { volts, at: new Date(latest) }
}

// gridVoltageFrom picks the best fresh reading across the snapshots
// given (the site itself, then its edge shadow): meter phases first.
export function gridVoltageFrom(snapshots: Array<CurrentResponse | null>, now: number): GridVoltage | null {
  const groups: Array<{ kind: GridVoltage['kind']; keys: string[] }> = [
    { kind: 'phase', keys: PHASE_KEYS },
    ...LINE_GROUPS.map((keys) => ({ kind: 'line' as const, keys })),
  ]
  for (const g of groups) {
    for (const snap of snapshots) {
      if (!snap) continue
      const hit = readGroup(snap, g.keys, now)
      if (hit) return { kind: g.kind, ...hit }
    }
  }
  return null
}

export function formatGridVoltage(v: GridVoltage): string {
  return `${v.volts.map((x) => Math.round(x)).join('/')} В`
}

export function gridVoltageTitle(v: GridVoltage): string {
  return v.kind === 'phase'
    ? 'Напруга фаз A/B/C на лічильнику мережі'
    : 'Лінійна напруга мережі AB/BC/CA (з боку інверторів)'
}
