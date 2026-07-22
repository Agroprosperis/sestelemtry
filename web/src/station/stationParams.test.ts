import { describe, expect, it } from 'vitest'
import {
  formatControlMode,
  formatParamDisplay,
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

  it('formats enum params via control-mode labels', () => {
    expect(formatParamDisplay(modeDef, 4)).toContain('Remote')
  })

  it('rounds counts to integers', () => {
    expect(formatParamDisplay(countDef, 8.0)).toBe('8')
  })
})
