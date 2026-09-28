import { useState, type FormEvent, type ReactNode } from 'react'
import { changePassword } from './authClient'
import { MIN_PASSWORD_LEN } from './permissions'
import './auth.css'

// PasswordForm changes the signed-in user's password: the three fields,
// their checks and the request. children is the heading above the
// fields; secondary is the button beside «Змінити пароль».
export function PasswordForm({
  children,
  secondary,
  onChanged,
  onBusyChange,
  dialog = false,
}: {
  children: ReactNode
  secondary: { label: string; onClick: () => void }
  onChanged: () => void
  onBusyChange?: (busy: boolean) => void
  // dialog marks the card as a modal dialog.
  dialog?: boolean
}) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if ([...next].length < MIN_PASSWORD_LEN) {
      setError(`Новий пароль має містити щонайменше ${MIN_PASSWORD_LEN} символів`)
      return
    }
    if (next !== repeat) {
      setError('Паролі не збігаються')
      return
    }
    setBusy(true)
    onBusyChange?.(true)
    setError(null)
    try {
      await changePassword(current, next)
      onChanged()
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Не вдалося змінити пароль')
    } finally {
      setBusy(false)
      onBusyChange?.(false)
    }
  }

  return (
    <form
      className="auth-card"
      role={dialog ? 'dialog' : undefined}
      aria-modal={dialog ? true : undefined}
      aria-label="Зміна пароля"
      onClick={(e) => e.stopPropagation()}
      onSubmit={(e) => void submit(e)}
    >
      {children}
      <label className="auth-field">
        <span>Поточний пароль</span>
        <input
          type="password"
          autoComplete="current-password"
          required
          autoFocus
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
        />
      </label>
      <label className="auth-field">
        <span>Новий пароль</span>
        <input
          type="password"
          autoComplete="new-password"
          required
          value={next}
          onChange={(e) => setNext(e.target.value)}
        />
      </label>
      <label className="auth-field">
        <span>Повторіть новий пароль</span>
        <input
          type="password"
          autoComplete="new-password"
          required
          value={repeat}
          onChange={(e) => setRepeat(e.target.value)}
        />
      </label>
      {error && (
        <div className="auth-error" role="alert">
          {error}
        </div>
      )}
      <div className="auth-actions">
        <button type="button" className="auth-secondary" onClick={secondary.onClick} disabled={busy}>
          {secondary.label}
        </button>
        <button type="submit" className="auth-primary" disabled={busy}>
          {busy ? 'Збереження…' : 'Змінити пароль'}
        </button>
      </div>
    </form>
  )
}
