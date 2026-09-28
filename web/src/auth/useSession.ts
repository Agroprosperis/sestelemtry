import { useCallback, useEffect, useMemo, useState } from 'react'
import { UNAUTHORIZED_EVENT } from '../api'
import type { AuthContextValue } from './authContext'
import { fetchMe, logout } from './authClient'
import { accessFor, type AuthMe } from './permissions'

export type Session =
  | { status: 'loading' }
  | { status: 'anonymous' }
  | { status: 'unreachable'; message: string }
  | { status: 'signed-in'; me: AuthMe }

export type SessionState = {
  session: Session
  // auth is what the signed-in app gets through AuthContext; null
  // until someone is signed in.
  auth: AuthContextValue | null
  // onSignedIn takes the /auth/me body the login form already received.
  onSignedIn: (me: AuthMe) => void
  // refresh asks the server for the session again.
  refresh: () => void
  signOut: () => Promise<void>
}

// useSession resolves who is signed in when the app boots.
export function useSession(): SessionState {
  const [session, setSession] = useState<Session>({ status: 'loading' })
  const [bootKey, setBootKey] = useState(0)

  useEffect(() => {
    const ac = new AbortController()
    fetchMe(ac.signal)
      .then((me) => setSession(me ? { status: 'signed-in', me } : { status: 'anonymous' }))
      .catch((e: unknown) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        setSession({
          status: 'unreachable',
          message: e instanceof TypeError ? 'Сервер не відповідає.' : e instanceof Error ? e.message : '',
        })
      })
    return () => ac.abort()
  }, [bootKey])

  // A session that expires or is revoked mid-use: reload rather than
  // swap in the login form, so nothing the previous session loaded
  // (module caches, open forms) outlives it.
  const signedIn = session.status === 'signed-in'
  useEffect(() => {
    if (!signedIn) return
    const onUnauthorized = () => window.location.reload()
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [signedIn])

  const signOut = useCallback(async () => {
    try {
      await logout()
    } finally {
      window.location.reload()
    }
  }, [])

  const onSignedIn = useCallback((me: AuthMe) => setSession({ status: 'signed-in', me }), [])

  const refresh = useCallback(() => {
    setSession({ status: 'loading' })
    setBootKey((k) => k + 1)
  }, [])

  const auth = useMemo<AuthContextValue | null>(
    () =>
      session.status === 'signed-in'
        ? { me: session.me, access: accessFor(session.me), logout: signOut }
        : null,
    [session, signOut],
  )

  return { session, auth, onSignedIn, refresh, signOut }
}
