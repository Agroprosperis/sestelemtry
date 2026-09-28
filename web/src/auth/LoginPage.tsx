import { useState, type FormEvent } from 'react'
import { login } from './authClient'
import type { AuthMe } from './permissions'
import './auth.css'

export function LoginPage({ onSignedIn }: { onSignedIn: (me: AuthMe) => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      onSignedIn(await login(email, password))
    } catch (err: unknown) {
      setError(
        err instanceof TypeError
          ? 'Не вдалося звʼязатися з сервером'
          : err instanceof Error
            ? err.message
            : 'Не вдалося увійти',
      )
      setBusy(false)
    }
  }

  return (
    <main className="auth-page">
      <form className="auth-card" onSubmit={(e) => void submit(e)}>
        <img src="/logo_agroprosperis.png" alt="Агропросперіс" className="auth-logo" />
        <h1>Вхід</h1>
        <label className="auth-field">
          <span>Email</span>
          <input
            type="email"
            autoComplete="username"
            required
            autoFocus
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
        <label className="auth-field">
          <span>Пароль</span>
          <input
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error && (
          <div className="auth-error" role="alert">
            {error}
          </div>
        )}
        <button type="submit" className="auth-primary" disabled={busy}>
          {busy ? 'Вхід…' : 'Увійти'}
        </button>
        <p>Обліковий запис і доступ до обʼєктів надає адміністратор.</p>
      </form>
    </main>
  )
}
