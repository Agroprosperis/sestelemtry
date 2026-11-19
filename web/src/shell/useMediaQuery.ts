import { useCallback, useMemo, useSyncExternalStore } from 'react'

// PHONE_QUERY is the phone breakpoint of the stylesheets, for the layout
// that is set in props rather than in CSS (a chart's axis ticks).
export const PHONE_QUERY = '(max-width: 640px)'

// useMediaQuery reports whether a CSS media query matches, and re-renders
// when that changes.
export function useMediaQuery(query: string): boolean {
  const list = useMemo(() => window.matchMedia(query), [query])
  const subscribe = useCallback(
    (onChange: () => void) => {
      list.addEventListener('change', onChange)
      return () => list.removeEventListener('change', onChange)
    },
    [list],
  )
  return useSyncExternalStore(subscribe, () => list.matches)
}
