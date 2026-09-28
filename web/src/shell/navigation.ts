import type { AppView } from '../auth/permissions'

// Views live in the `?view=` query parameter (App reads it), so moving
// between them keeps the rest of the query string (organization_id etc).

// navigateView opens another view without a full reload; tab selects a
// tab inside it.
export function navigateView(view: AppView, tab?: string) {
  window.history.pushState({}, '', viewURL(view, tab))
  window.dispatchEvent(new PopStateEvent('popstate'))
}

// replaceView points the URL at a view the user may open, without
// adding a history entry for the one they may not.
export function replaceView(view: AppView) {
  window.history.replaceState({}, '', viewURL(view))
}

function viewURL(view: AppView, tab?: string): URL {
  const url = new URL(window.location.href)
  if (view === 'dashboard') url.searchParams.delete('view')
  else url.searchParams.set('view', view)
  if (tab) url.searchParams.set('tab', tab)
  else url.searchParams.delete('tab')
  return url
}
