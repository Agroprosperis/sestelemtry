import type { ReactNode } from 'react'
import './auth.css'

// AuthNotice is the full-page card for states where the app has nothing
// to show yet: the server is unreachable, or the account has no role.
export function AuthNotice({
  title,
  children,
  actions,
}: {
  title: string
  children: ReactNode
  actions: ReactNode
}) {
  return (
    <main className="auth-page">
      <section className="auth-card" role="status">
        <img src="/logo_agroprosperis.png" alt="Агропросперіс" className="auth-logo" />
        <h1>{title}</h1>
        <p>{children}</p>
        <div className="auth-actions">{actions}</div>
      </section>
    </main>
  )
}
