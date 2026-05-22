import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchOrgTariffs, saveOrgTariffs } from '../api'
import { DEFAULT_TARIFFS, type Tariffs } from './tariffs'

// Save status surfaced to the EconomicsHeader so the operator gets a
// passive confirmation that their last edit hit the backend. Kept as
// a small string union (instead of a boolean pair) so the UI can
// render distinct "saving" / "saved" / "error" hints — and so future
// states like "offline" stay backwards-compatible.
export type OrgTariffsStatus = 'loading' | 'idle' | 'saving' | 'saved' | 'error'

// Debounce window for autosaves. 800 ms is long enough that a typist
// dragging an input value through several keystrokes only triggers
// one PUT, but short enough that the operator sees the "Збережено"
// hint flip almost as soon as they stop editing.
const SAVE_DEBOUNCE_MS = 800

// "Saved" status auto-clears after this delay so the indicator
// returns to a neutral state instead of permanently saying
// "Збережено" (which would gradually become meaningless).
const SAVED_STICKY_MS = 1500

type State = {
  tariffs: Tariffs
  status: OrgTariffsStatus
  error: string | null
}

// useOrgTariffs owns the per-org tariff lifecycle: it loads the
// persisted bundle when `organizationID` changes, exposes a setter
// the form binds to, and PUTs back to the API on a 800 ms debounce.
//
// The hook is the single source of truth for tariffs on the
// economics page — replacing the previous URL-based persistence —
// so an org switch transparently moves the form to that elevator's
// saved settings (or DEFAULT_TARIFFS for a brand-new org). A 404 on
// load is *not* treated as an error: it means "no row yet", and the
// hook seeds DEFAULT_TARIFFS without auto-saving so an analyst who
// only browsed the page never ends up persisting accidental
// defaults.
//
// Aborts and races: every fetch (load + save) is wrapped in an
// AbortController that fires on org switch and on unmount. A pending
// debounced save is also dropped on org switch so the previous org
// can't clobber the freshly-loaded settings of the new one.
export function useOrgTariffs(organizationID: string) {
  const [state, setState] = useState<State>({
    tariffs: DEFAULT_TARIFFS,
    status: 'loading',
    error: null,
  })

  // We keep the latest tariffs in a ref so the debounced save closure
  // always sees the freshest value without being part of the timer's
  // dependency array (which would re-arm on every keystroke and
  // defeat the debounce).
  const latestRef = useRef<Tariffs>(DEFAULT_TARIFFS)
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const savedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const saveControllerRef = useRef<AbortController | null>(null)
  const orgIDRef = useRef(organizationID)
  // True once the initial load completed for the current org. We
  // skip auto-save until then so loading defaults for a fresh org
  // (404 path) doesn't immediately PUT them back.
  const dirtyAllowedRef = useRef(false)

  const cancelPendingSave = useCallback(() => {
    if (saveTimerRef.current !== null) {
      clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    if (saveControllerRef.current) {
      saveControllerRef.current.abort()
      saveControllerRef.current = null
    }
  }, [])

  // Load on org change. AbortController scope guards the whole
  // effect so a fast org switch can't race a slow GET into the
  // wrong setState. All setState calls happen inside the async
  // worker (not the effect body) so we sidestep the React 19
  // "set-state-in-effect" lint and avoid the cascading-render
  // pattern it flags — the brief microtask delay before the
  // "loading" badge appears is imperceptible in practice.
  useEffect(() => {
    orgIDRef.current = organizationID
    dirtyAllowedRef.current = false
    cancelPendingSave()
    if (savedTimerRef.current !== null) {
      clearTimeout(savedTimerRef.current)
      savedTimerRef.current = null
    }
    const controller = new AbortController()
    let cancelled = false
    ;(async () => {
      if (cancelled) return
      setState((s) => ({ ...s, status: 'loading', error: null }))
      try {
        const loaded = await fetchOrgTariffs(organizationID, controller.signal)
        if (cancelled) return
        const next = loaded ?? DEFAULT_TARIFFS
        latestRef.current = next
        setState({ tariffs: next, status: 'idle', error: null })
        dirtyAllowedRef.current = true
      } catch (e) {
        if (controller.signal.aborted) return
        if (cancelled) return
        const msg = e instanceof Error ? e.message : 'Не вдалося завантажити тарифи'
        // Even on error we seed defaults so the UI is usable; the
        // status flag tells the header to render the failure.
        latestRef.current = DEFAULT_TARIFFS
        setState({ tariffs: DEFAULT_TARIFFS, status: 'error', error: msg })
        dirtyAllowedRef.current = true
      }
    })()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [organizationID, cancelPendingSave])

  // Cleanup on unmount.
  useEffect(() => {
    return () => {
      if (saveTimerRef.current !== null) clearTimeout(saveTimerRef.current)
      if (savedTimerRef.current !== null) clearTimeout(savedTimerRef.current)
      saveControllerRef.current?.abort()
    }
  }, [])

  const scheduleSave = useCallback(() => {
    if (!dirtyAllowedRef.current) return
    if (saveTimerRef.current !== null) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => {
      saveTimerRef.current = null
      const orgID = orgIDRef.current
      const payload = latestRef.current
      saveControllerRef.current?.abort()
      const controller = new AbortController()
      saveControllerRef.current = controller
      setState((s) => ({ ...s, status: 'saving', error: null }))
      ;(async () => {
        try {
          await saveOrgTariffs(orgID, payload, controller.signal)
          if (controller.signal.aborted) return
          // Stop early if a different org was loaded mid-flight.
          if (orgIDRef.current !== orgID) return
          setState((s) => ({ ...s, status: 'saved', error: null }))
          if (savedTimerRef.current !== null) clearTimeout(savedTimerRef.current)
          savedTimerRef.current = setTimeout(() => {
            savedTimerRef.current = null
            setState((s) => (s.status === 'saved' ? { ...s, status: 'idle' } : s))
          }, SAVED_STICKY_MS)
        } catch (e) {
          if (controller.signal.aborted) return
          if (orgIDRef.current !== orgID) return
          const msg = e instanceof Error ? e.message : 'Не вдалося зберегти тарифи'
          setState((s) => ({ ...s, status: 'error', error: msg }))
        }
      })()
    }, SAVE_DEBOUNCE_MS)
  }, [])

  const setTariffs = useCallback(
    (next: Tariffs) => {
      latestRef.current = next
      setState((s) => ({ ...s, tariffs: next }))
      scheduleSave()
    },
    [scheduleSave],
  )

  return {
    tariffs: state.tariffs,
    status: state.status,
    error: state.error,
    setTariffs,
  }
}
