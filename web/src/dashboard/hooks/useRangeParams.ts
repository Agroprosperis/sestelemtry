import { useCallback, useMemo, useState } from 'react'
import { startOfPeriod, type RangePreset } from '../range'

const VALID_PRESETS: ReadonlyArray<RangePreset> = ['day', 'month', 'year']

function isPreset(value: string): value is RangePreset {
  return (VALID_PRESETS as ReadonlyArray<string>).includes(value)
}

// readSearch runs once on mount; we deliberately do NOT subscribe to
// popstate / hashchange because the dashboard doesn't navigate away —
// browser back/forward through user-driven preset changes would surprise
// users. URL sync is one-way: state -> URL.
function readSearch(now: Date): { preset: RangePreset; anchor: Date } {
  const search = new URLSearchParams(window.location.search)
  const rawPreset = (search.get('preset') ?? '').toLowerCase()
  const preset = isPreset(rawPreset) ? rawPreset : 'day'
  const rawAnchor = (search.get('anchor') ?? '').trim()
  let anchor = startOfPeriod(preset, now)
  if (rawAnchor) {
    const parsed = new Date(rawAnchor)
    if (!Number.isNaN(parsed.getTime())) {
      anchor = startOfPeriod(preset, parsed)
    }
  }
  return { preset, anchor }
}

function writeSearch(preset: RangePreset, anchor: Date): void {
  const url = new URL(window.location.href)
  url.searchParams.set('preset', preset)
  // Use date-only ISO so refreshing the URL is timezone-robust: the
  // anchor is interpreted as the local-tz start of that calendar day,
  // matching how startOfPeriod normalises it.
  const y = anchor.getFullYear()
  const m = String(anchor.getMonth() + 1).padStart(2, '0')
  const d = String(anchor.getDate()).padStart(2, '0')
  url.searchParams.set('anchor', `${y}-${m}-${d}`)
  window.history.replaceState({}, '', url)
}

// useRangeParams owns the active preset + anchor and keeps them in sync
// with the URL so refresh / share-link preserves the selection.
//
// `change`-style callbacks are stable across renders so they can sit in
// effect dependency arrays without retriggering.
export function useRangeParams() {
  const initial = useMemo(() => readSearch(new Date()), [])
  const [preset, setPresetState] = useState<RangePreset>(initial.preset)
  const [anchor, setAnchorState] = useState<Date>(initial.anchor)

  const setPreset = useCallback((next: RangePreset) => {
    const nextAnchor = startOfPeriod(next, new Date())
    setPresetState(next)
    setAnchorState(nextAnchor)
    writeSearch(next, nextAnchor)
  }, [])

  const setAnchor = useCallback(
    (next: Date) => {
      const nextAnchor = startOfPeriod(preset, next)
      setAnchorState(nextAnchor)
      writeSearch(preset, nextAnchor)
    },
    [preset],
  )

  return { preset, anchor, setPreset, setAnchor }
}
