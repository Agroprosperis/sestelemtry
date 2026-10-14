import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { usePeriodAnchor } from './usePeriodAnchor'

describe('usePeriodAnchor', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/')
  })

  it('starts every block from the day anchor of an older link', () => {
    window.history.replaceState({}, '', '/?preset=month&anchor=2026-08-15')
    const day = renderHook(() => usePeriodAnchor('day')).result.current.anchor
    const month = renderHook(() => usePeriodAnchor('month')).result.current.anchor
    const year = renderHook(() => usePeriodAnchor('year')).result.current.anchor
    expect(day).toEqual(new Date(2026, 7, 15))
    expect(month).toEqual(new Date(2026, 7, 1))
    expect(year).toEqual(new Date(2026, 0, 1))
  })

  it('prefers the block’s own key over the day anchor', () => {
    window.history.replaceState({}, '', '/?anchor=2026-08-15&month=2026-03&year=2025')
    expect(renderHook(() => usePeriodAnchor('month')).result.current.anchor).toEqual(new Date(2026, 2, 1))
    expect(renderHook(() => usePeriodAnchor('year')).result.current.anchor).toEqual(new Date(2025, 0, 1))
  })

  it('writes its own key and drops the stale preset', () => {
    window.history.replaceState({}, '', '/?organization_id=ze&preset=year&anchor=2026-08-15')
    const { result } = renderHook(() => usePeriodAnchor('month'))
    act(() => result.current.setAnchor(new Date(2026, 4, 20)))
    expect(result.current.anchor).toEqual(new Date(2026, 4, 1))
    const search = new URLSearchParams(window.location.search)
    expect(search.get('month')).toBe('2026-05')
    expect(search.get('anchor')).toBe('2026-08-15')
    expect(search.get('organization_id')).toBe('ze')
    expect(search.has('preset')).toBe(false)
  })
})
