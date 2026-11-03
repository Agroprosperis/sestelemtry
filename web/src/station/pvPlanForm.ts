import type { PvPlanSource } from '../api'
import type { PvOrientation, PvStation, PvStationChange } from './pvPlanClient'

// The passport form keeps every input as typed text, so a half-entered
// number ("0," on the way to "0,29") doesn't snap back under the
// cursor. parsePvPlanForm turns it into a PvStation only on save.

export type PvPlanRow = {
  // key identifies the row in the editor, independent of the plane
  // index the engineer can renumber.
  key: number
  idx: string
  tilt: string
  azimuth: string
  modules: string
  moduleW: string
  enabled: boolean
  note: string
}

export type PvPlanForm = {
  source: PvPlanSource
  latitude: string
  longitude: string
  pacLimitKw: string
  gamma: string
  wholeElevatorFrom: string
  rows: PvPlanRow[]
}

let lastRowKey = 0

export function nextRowKey(): number {
  lastRowKey += 1
  return lastRowKey
}

function text(v: number | null | undefined): string {
  return v == null || !Number.isFinite(v) ? '' : String(v).replace('.', ',')
}

export function formFromStation(st: PvStation | null): PvPlanForm {
  return {
    source: st?.source ?? 'n8n',
    latitude: text(st?.latitude),
    longitude: text(st?.longitude),
    pacLimitKw: text(st?.pac_limit_kw || null),
    gamma: text(st?.gamma_pct_per_c ?? null),
    wholeElevatorFrom: st?.whole_elevator_from ?? '',
    rows: (st?.orientations ?? []).map((o) => ({
      key: nextRowKey(),
      idx: String(o.idx),
      tilt: text(o.tilt_deg),
      azimuth: text(o.azimuth_deg),
      modules: String(o.modules_count),
      moduleW: text(o.module_power_w),
      enabled: o.enabled,
      note: o.note ?? '',
    })),
  }
}

// emptyRow continues the previous plane's module type and numbering,
// which is how a station is usually entered: one module model, planes
// numbered in order.
export function emptyRow(rows: PvPlanRow[]): PvPlanRow {
  const last = rows[rows.length - 1]
  const maxIdx = rows.reduce((m, r) => Math.max(m, Number(r.idx) || 0), 0)
  return {
    key: nextRowKey(),
    idx: String(maxIdx + 1),
    tilt: '',
    azimuth: '',
    modules: '',
    moduleW: last?.moduleW ?? '',
    enabled: true,
    note: '',
  }
}

function num(raw: string): number | null {
  const t = raw.trim().replace(',', '.')
  if (t === '') return null
  const v = Number(t)
  return Number.isFinite(v) ? v : NaN
}

// rowPdcKw is the plane's DC nameplate, or null while its inputs are
// incomplete.
export function rowPdcKw(row: PvPlanRow): number | null {
  const modules = num(row.modules)
  const w = num(row.moduleW)
  if (modules == null || w == null || !Number.isFinite(modules) || !Number.isFinite(w)) return null
  return (modules * w) / 1000
}

export function rowsPdcTotalKwp(rows: PvPlanRow[]): number {
  return rows.reduce((sum, r) => (r.enabled ? sum + (rowPdcKw(r) ?? 0) : sum), 0)
}

export function formPdcTotalKwp(form: PvPlanForm): number {
  return rowsPdcTotalKwp(form.rows)
}

// formModelled mirrors pvplan.Station.Modelled: enough of a passport
// for the in-house model to run.
export function formModelled(form: PvPlanForm): boolean {
  const pac = num(form.pacLimitKw)
  return pac != null && pac > 0 && formPdcTotalKwp(form) > 0
}

// RATED_MISMATCH is how far the planes' nameplate may stray from the
// rated PV power FusionSolar reports before the passport warns: past
// it, a plane is probably missing or entered twice.
export const RATED_MISMATCH = 0.03

export function ratedMismatch(totalKwp: number, ratedKw: number | null): number | null {
  if (ratedKw == null || !(ratedKw > 0) || !(totalKwp > 0)) return null
  const share = Math.abs(totalKwp - ratedKw) / ratedKw
  return share > RATED_MISMATCH ? share : null
}

export type ParsedPvPlanForm = { station: PvStation; error: null } | { station: null; error: string }

// parsePvPlanForm checks what the browser can and builds the PvStation
// to save. The API validates again and has the final word.
export function parsePvPlanForm(organizationID: string, form: PvPlanForm): ParsedPvPlanForm {
  const fail = (error: string): ParsedPvPlanForm => ({ station: null, error })
  const lat = num(form.latitude)
  const lon = num(form.longitude)
  if ((lat == null) !== (lon == null)) return fail('Координати задаються парою: і широта, і довгота, або жодної.')
  if (lat != null && (!Number.isFinite(lat) || lat < -90 || lat > 90)) return fail('Широта має бути від −90 до 90.')
  if (lon != null && (!Number.isFinite(lon) || lon < -180 || lon > 180)) return fail('Довгота має бути від −180 до 180.')
  const pac = num(form.pacLimitKw)
  if (pac != null && (!Number.isFinite(pac) || pac < 0)) return fail('Ліміт інвертора має бути додатним числом.')
  const gamma = num(form.gamma)
  if (gamma != null && (!Number.isFinite(gamma) || gamma < -2 || gamma > 0)) {
    return fail('Температурний коефіцієнт має бути від −2 до 0 %/°C.')
  }
  const orientations: PvOrientation[] = []
  const seen = new Set<number>()
  for (const r of form.rows) {
    const idx = num(r.idx)
    if (idx == null || !Number.isInteger(idx) || idx < 1) return fail('Номер орієнтації має бути цілим числом від 1.')
    if (seen.has(idx)) return fail(`Орієнтація №${idx} повторюється.`)
    seen.add(idx)
    const tilt = num(r.tilt)
    if (tilt == null || !Number.isFinite(tilt) || tilt < 0 || tilt > 90) {
      return fail(`Орієнтація №${idx}: нахил має бути від 0 до 90°.`)
    }
    const azimuth = num(r.azimuth)
    if (azimuth == null || !Number.isFinite(azimuth) || azimuth < -180 || azimuth > 180) {
      return fail(`Орієнтація №${idx}: азимут має бути від −180 до 180°.`)
    }
    const modules = num(r.modules)
    if (modules == null || !Number.isInteger(modules) || modules < 1) {
      return fail(`Орієнтація №${idx}: кількість модулів має бути цілим числом від 1.`)
    }
    const w = num(r.moduleW)
    if (w == null || !Number.isFinite(w) || w <= 0 || w > 1000) {
      return fail(`Орієнтація №${idx}: потужність модуля має бути від 1 до 1000 Вт.`)
    }
    orientations.push({
      idx,
      tilt_deg: tilt,
      azimuth_deg: azimuth,
      modules_count: modules,
      module_power_w: w,
      enabled: r.enabled,
      note: r.note.trim() || undefined,
    })
  }
  if (orientations.length > 0 && !(pac != null && pac > 0)) return fail('Ліміт інвертора має бути більшим за 0 кВт.')
  if (form.wholeElevatorFrom && !/^\d{4}-\d{2}-\d{2}$/.test(form.wholeElevatorFrom)) {
    return fail('Дата «План усього елеватора з» має бути у форматі РРРР-ММ-ДД.')
  }
  orientations.sort((a, b) => a.idx - b.idx)
  const station: PvStation = {
    organization_id: organizationID,
    source: form.source,
    latitude: lat,
    longitude: lon,
    pac_limit_kw: pac ?? 0,
    gamma_pct_per_c: gamma ?? 0,
    whole_elevator_from: form.wholeElevatorFrom || undefined,
    orientations,
  }
  if (station.source === 'model' && !formModelled(form)) {
    return fail('Для власної моделі потрібні ліміт інвертора і хоча б одна увімкнена орієнтація.')
  }
  return { station, error: null }
}

export const SOURCE_LABELS: Record<PvPlanSource, string> = {
  n8n: 'n8n',
  model: 'власна модель',
}

const FIELD_LABELS: Record<string, string> = {
  source: 'Джерело плану',
  latitude: 'Широта',
  longitude: 'Довгота',
  pac_limit_kw: 'Ліміт інвертора, кВт',
  gamma_pct_per_c: 'Темп. коефіцієнт, %/°C',
  whole_elevator_from: 'План усього елеватора з',
  tilt_deg: 'нахил, °',
  azimuth_deg: 'азимут, °',
  modules_count: 'модулів',
  module_power_w: 'Вт модуля',
  enabled: 'увімкнено',
  note: 'примітка',
}

function changeValue(field: string, v: unknown): string {
  if (v === null || v === undefined || v === '') return '—'
  if (field === 'source' && (v === 'n8n' || v === 'model')) return SOURCE_LABELS[v]
  if (typeof v === 'boolean') return v ? 'так' : 'ні'
  if (typeof v === 'number') return String(v).replace('.', ',')
  return String(v)
}

// describeChange renders one journal entry line in Ukrainian.
export function describeChange(c: PvStationChange): string {
  if (c.field === 'seeded') return 'Стартове заповнення з таблиці орієнтацій n8n'
  if (c.field === 'orientation_added') return `Додано орієнтацію №${c.orientation}`
  if (c.field === 'orientation_removed') return `Видалено орієнтацію №${c.orientation}`
  const label = FIELD_LABELS[c.field] ?? c.field
  const where = c.orientation ? `Орієнтація №${c.orientation}: ${label}` : label
  return `${where}: ${changeValue(c.field, c.from)} → ${changeValue(c.field, c.to)}`
}

export function describeAuthor(by: string | undefined): string {
  if (!by) return 'невідомо'
  return by === 'seed' ? 'система' : by
}

// formatDateTime renders a journal or rebuild instant in local time.
export function formatDateTime(iso: string | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('uk-UA', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}
