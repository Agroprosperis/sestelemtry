import { describe, expect, it } from 'vitest'
import type { PvStation } from './pvPlanClient'
import {
  describeChange,
  emptyRow,
  formFromStation,
  formModelled,
  formPdcTotalKwp,
  parsePvPlanForm,
  ratedMismatch,
} from './pvPlanForm'

const pde: PvStation = {
  organization_id: 'pde',
  source: 'n8n',
  latitude: 49.9993363781658,
  longitude: 26.386386689961547,
  pac_limit_kw: 250,
  gamma_pct_per_c: -0.28,
  orientations: [
    { idx: 1, tilt_deg: 30, azimuth_deg: -13.3, modules_count: 504, module_power_w: 620, enabled: true },
  ],
}

describe('pvPlanForm', () => {
  it('round-trips a passport through the form', () => {
    const parsed = parsePvPlanForm('pde', formFromStation(pde))
    expect(parsed.error).toBeNull()
    expect(parsed.station).toEqual({ ...pde, whole_elevator_from: undefined, orientations: [{ ...pde.orientations[0], note: undefined }] })
  })

  // Engineers type decimals with a comma.
  it('reads comma decimals', () => {
    const form = formFromStation(pde)
    form.gamma = '-0,29'
    form.rows[0].azimuth = '-92,5'
    const parsed = parsePvPlanForm('pde', form)
    expect(parsed.station?.gamma_pct_per_c).toBe(-0.29)
    expect(parsed.station?.orientations[0].azimuth_deg).toBe(-92.5)
  })

  it('sums the enabled planes from modules and module watts', () => {
    const form = formFromStation(pde)
    form.rows.push({ ...emptyRow(form.rows), tilt: '10', azimuth: '0', modules: '100', moduleW: '620', enabled: false })
    expect(formPdcTotalKwp(form)).toBeCloseTo(312.48)
    expect(formModelled(form)).toBe(true)
  })

  it('continues the module type and numbering for a new plane', () => {
    const form = formFromStation(pde)
    const row = emptyRow(form.rows)
    expect(row.idx).toBe('2')
    expect(row.moduleW).toBe('620')
  })

  it.each([
    ['a repeated plane number', (f: ReturnType<typeof formFromStation>) => f.rows.push({ ...f.rows[0], key: 99 })],
    ['a tilt past vertical', (f: ReturnType<typeof formFromStation>) => (f.rows[0].tilt = '95')],
    ['half a coordinate pair', (f: ReturnType<typeof formFromStation>) => (f.longitude = '')],
    ['planes without an inverter limit', (f: ReturnType<typeof formFromStation>) => (f.pacLimitKw = '')],
  ])('refuses %s', (_name, spoil) => {
    const form = formFromStation(pde)
    spoil(form)
    expect(parsePvPlanForm('pde', form).error).not.toBeNull()
  })

  it('refuses the model for a station without planes', () => {
    const form = formFromStation({ ...pde, source: 'model', orientations: [] })
    expect(parsePvPlanForm('pde', form).error).toMatch(/власної моделі/)
  })

  it('warns only past the 3 % nameplate mismatch', () => {
    expect(ratedMismatch(312.48, 312)).toBeNull()
    expect(ratedMismatch(312.48, 250)).toBeCloseTo(0.25, 2)
    expect(ratedMismatch(312.48, null)).toBeNull()
  })

  it('describes journal entries in Ukrainian', () => {
    expect(describeChange({ field: 'source', from: 'n8n', to: 'model' })).toBe('Джерело плану: n8n → власна модель')
    expect(describeChange({ field: 'tilt_deg', orientation: 2, from: 3.6, to: 4 })).toBe('Орієнтація №2: нахил, °: 3,6 → 4')
    expect(describeChange({ field: 'orientation_added', orientation: 3 })).toBe('Додано орієнтацію №3')
    expect(describeChange({ field: 'seeded' })).toMatch(/Стартове заповнення/)
  })
})
