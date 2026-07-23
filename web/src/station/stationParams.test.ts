import { describe, expect, it } from 'vitest'
import {
  formatControlMode,
  formatParamDisplay,
  formatParamUnit,
  formatQualityFlag,
  STATION_PARAMS,
} from './stationParams'

describe('formatControlMode', () => {
  it('labels remote scheduling mode 4', () => {
    expect(formatControlMode(4)).toContain('Remote communication scheduling')
    expect(formatControlMode(4)).toMatch(/^4/)
  })

  it('falls back to the numeric code for unknown modes', () => {
    expect(formatControlMode(42)).toBe('42')
  })

  it('renders an em dash for null', () => {
    expect(formatControlMode(null)).toBe('—')
  })
})

describe('formatQualityFlag', () => {
  it('translates known flags', () => {
    expect(formatQualityFlag('CONTROL_MODE_NOT_REMOTE')).toMatch(/Remote/)
  })

  it('passes through unknown flags', () => {
    expect(formatQualityFlag('CUSTOM_FLAG')).toBe('CUSTOM_FLAG')
  })
})

describe('formatParamDisplay', () => {
  const modeDef = STATION_PARAMS.find((d) => d.key === 'active_power_control_mode')!
  const countDef = STATION_PARAMS.find((d) => d.key === 'ess_count')!
  const pvDef = STATION_PARAMS.find((d) => d.key === 'pv_rated_kw')!
  const essKwDef = STATION_PARAMS.find((d) => d.key === 'ess_rated_kw')!
  const essKwhDef = STATION_PARAMS.find((d) => d.key === 'ess_rated_kwh')!

  it('formats enum params via control-mode labels', () => {
    expect(formatParamDisplay(modeDef, 4)).toContain('Remote')
  })

  it('rounds counts to integers', () => {
    expect(formatParamDisplay(countDef, 8.0)).toBe('8')
  })

  it('keeps power ≤1000 kW in kW', () => {
    expect(formatParamDisplay(pvDef, 550)).toBe('550')
    expect(formatParamUnit(pvDef, 550)).toBe('кВт')
    expect(formatParamDisplay(pvDef, 1000)).toBe('1000')
    expect(formatParamUnit(pvDef, 1000)).toBe('кВт')
  })

  it('shows power >1000 kW in MW', () => {
    expect(formatParamDisplay(pvDef, 1500)).toBe('1.5')
    expect(formatParamUnit(pvDef, 1500)).toBe('МВт')
    expect(formatParamDisplay(essKwDef, 2200)).toBe('2.2')
    expect(formatParamUnit(essKwDef, 2200)).toBe('МВт')
  })

  it('does not convert capacity kWh to MW', () => {
    expect(formatParamDisplay(essKwhDef, 1500)).toBe('1500')
    expect(formatParamUnit(essKwhDef, 1500)).toBe('кВт·год')
  })
})
