import type { PlantInventory } from '../types'

export type StationParamGroup = 'passport' | 'ops'

export type StationParamKey =
  | 'pv_rated_kw'
  | 'ess_rated_kw'
  | 'ess_rated_kwh'
  | 'ess_count'
  | 'pcs_count'
  | 'ess_soh_pct'
  | 'active_power_control_mode'

export type StationIconId =
  | 'pv'
  | 'essPower'
  | 'essEnergy'
  | 'cabinets'
  | 'pcs'
  | 'soh'
  | 'mode'
  | 'meta'

export type StationParamDef = {
  key: StationParamKey
  label: string
  unit: string
  group: StationParamGroup
  icon: StationIconId
  /** When true, value is an enum (control mode) rather than a number. */
  enum?: boolean
}

// STATION_PARAMS is the extensible catalog of organization passport /
// ops fields shown on the station page. Add a row here (and map it in
// readParamValue) when collector/API expose a new inventory field.
export const STATION_PARAMS: StationParamDef[] = [
  {
    key: 'pv_rated_kw',
    label: 'Номінальна потужність СЕС',
    unit: 'кВт',
    group: 'passport',
    icon: 'pv',
  },
  {
    key: 'ess_rated_kw',
    label: 'Номінальна потужність УЗЕ',
    unit: 'кВт',
    group: 'passport',
    icon: 'essPower',
  },
  {
    key: 'ess_rated_kwh',
    label: 'Номінальна ємність УЗЕ',
    unit: 'кВт·год',
    group: 'passport',
    icon: 'essEnergy',
  },
  {
    key: 'ess_count',
    label: 'Кількість шаф ESS',
    unit: 'шт',
    group: 'passport',
    icon: 'cabinets',
  },
  {
    key: 'pcs_count',
    label: 'Кількість PCS',
    unit: 'шт',
    group: 'passport',
    icon: 'pcs',
  },
  {
    key: 'ess_soh_pct',
    label: 'SOH',
    unit: '%',
    group: 'passport',
    icon: 'soh',
  },
  {
    key: 'active_power_control_mode',
    label: 'Режим керування акт. потужністю',
    unit: '',
    group: 'ops',
    icon: 'mode',
    enum: true,
  },
]

export const GROUP_LABELS: Record<StationParamGroup, string> = {
  passport: 'Паспорт',
  ops: 'Службові',
}

// Active power control mode (register 40737) — Issue 52 / handoff.
const CONTROL_MODE_LABELS: Record<number, string> = {
  0: 'Без обмежень',
  1: 'DI scheduling',
  3: 'Обмеження у % (open loop)',
  4: 'Remote communication scheduling',
  6: 'Обмеження потужності підключення (кВт)',
  200: 'Remote output control',
  65533: 'Slave SmartLogger',
  65534: 'Без scheduling',
}

export function formatControlMode(mode: number | null | undefined): string {
  if (mode == null || Number.isNaN(mode)) return '—'
  const n = Math.round(mode)
  const label = CONTROL_MODE_LABELS[n]
  return label ? `${n} — ${label}` : String(n)
}

export const QUALITY_FLAG_LABELS: Record<string, string> = {
  CONTROL_MODE_NOT_REMOTE: 'Режим керування не Remote (потрібен 4)',
  CONTROL_MODE_DISAGREE: 'Різний режим на PV і ESS SmartLogger',
  MODBUS_ERROR: 'Помилка читання Modbus',
}

export function formatQualityFlag(flag: string): string {
  return QUALITY_FLAG_LABELS[flag] ?? flag
}

export const POLL_REASON_LABELS: Record<string, string> = {
  startup: 'старт collector',
  hourly: 'погодинний',
  daily: 'добовий',
}

export function formatPollReason(reason: string): string {
  return POLL_REASON_LABELS[reason] ?? reason
}

export function readParamValue(inv: PlantInventory, key: StationParamKey): number | null {
  switch (key) {
    case 'pv_rated_kw':
      return inv.pv_rated_kw
    case 'ess_rated_kw':
      return inv.ess_rated_kw
    case 'ess_rated_kwh':
      return inv.ess_rated_kwh
    case 'ess_count':
      return inv.ess_count
    case 'pcs_count':
      return inv.pcs_count
    case 'ess_soh_pct':
      return inv.ess_soh_pct
    case 'active_power_control_mode':
      return inv.active_power_control_mode
    default:
      return null
  }
}

export function formatParamDisplay(def: StationParamDef, value: number | null): string {
  if (value == null || Number.isNaN(value)) return '—'
  if (def.enum) return formatControlMode(value)
  if (def.key === 'ess_count' || def.key === 'pcs_count') {
    return String(Math.round(value))
  }
  const rounded = Math.round(value * 10) / 10
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1)
}

/** Compact display for history from/to cells (no long enum prose). */
export function formatHistoryValue(def: StationParamDef, value: number | null): string {
  if (value == null || Number.isNaN(value)) return '—'
  if (def.enum) {
    const n = Math.round(value)
    const label = CONTROL_MODE_LABELS[n]
    return label ? `${n}` : String(n)
  }
  return formatParamDisplay(def, value)
}
