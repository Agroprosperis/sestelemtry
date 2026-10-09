// Helpers shared between the standalone Import page and the DAM
// price loader card mounted on the Economics page. Kept here so both
// surfaces stay byte-identical (same Kyiv-time date math, same abort
// detection); when one is tweaked the other follows.

// isAbortError detects a fetch cancelled by the operator's "cancel"
// button (AbortController.abort), so the UI shows a neutral
// "скасовано" note instead of a red error banner.
export function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

// RunState is the small state machine driving the import / fetch
// buttons: idle (clickable), loading (in-flight, cancel available),
// done (result panel visible), error (red banner with the message).
export type RunState = 'idle' | 'loading' | 'done' | 'error'

// kyivDate returns a YYYY-MM-DD calendar date in Europe/Kyiv,
// shifted by `offsetDays` from now. The dashboard, economics and
// import surfaces all anchor to local Ukraine time, so the same
// helper is reused to avoid an off-by-one day at the timezone seam.
export function kyivDate(offsetDays: number): string {
  const fmt = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Europe/Kyiv',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
  const d = new Date()
  d.setUTCDate(d.getUTCDate() + offsetDays)
  return fmt.format(d)
}
