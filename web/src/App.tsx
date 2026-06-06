import { useEffect, useState } from 'react'
import { Dashboard } from './dashboard/Dashboard'
import { EconomicsPage } from './economics/EconomicsPage'
import { ImportPage } from './import/ImportPage'

type View = 'dashboard' | 'economics' | 'import'

// readView reads the `?view=` query parameter on every render and
// returns the active page id. We deliberately avoid pulling in
// `react-router-dom` for what is currently a small-set-of-pages
// dashboard: the fewer client-side dependencies, the smaller the
// initial JS payload (and the simpler the offline / PWA story).
function readView(): View {
  if (typeof window === 'undefined') return 'dashboard'
  const params = new URLSearchParams(window.location.search)
  const view = params.get('view')
  if (view === 'economics') return 'economics'
  if (view === 'import') return 'import'
  return 'dashboard'
}

function App() {
  const [view, setView] = useState<View>(readView)

  // Listen for back/forward navigation so the lightweight query-param
  // routing still feels native: hitting the back button after going to
  // ?view=economics returns to the main dashboard without a full page
  // reload. The header switch link below uses `pushState` directly to
  // get the same behaviour going forward.
  useEffect(() => {
    const handler = () => setView(readView())
    window.addEventListener('popstate', handler)
    return () => window.removeEventListener('popstate', handler)
  }, [])

  if (view === 'economics') {
    return <EconomicsPage />
  }
  if (view === 'import') {
    return <ImportPage />
  }
  return <Dashboard />
}

export default App
