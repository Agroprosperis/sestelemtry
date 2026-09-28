import { useEffect, useState, type FormEvent } from 'react'
import { changePassword } from './authClient'
import './auth.css'

const MIN_PASSWORD_LEN = 10

// PasswordDialog changes the signed-in user's password. forced is the
// full-page variant for the factory admin/admin: no way around it but
// signing out, and onClose runs right after a successful change.
export function PasswordDialog({
  onClose,
  forced = false,
  onSignOut,
}: {
  onClose: () => void
  forced?: boolean
  onSignOut?: () => void
}) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)

  useEffect(() => {
    if (forced) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [busy, forced, onClose])

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
    setError(null)
    try {
      await changePassword(current, next)
      if (forced) {
        onClose()
        return
      }
      setDone(true)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Не вдалося змінити пароль')
    } finally {
      setBusy(false)
    }
  }

  const form = (
    <form
      className="auth-card"
      role={forced ? undefined : 'dialog'}
      aria-modal={forced ? undefined : true}
      aria-label="Зміна пароля"
      onClick={(e) => e.stopPropagation()}
      onSubmit={(e) => void submit(e)}
    >
      {forced ? <img src="/logo_agroprosperis.png" alt="Агропросперіс" className="auth-logo" /> : null}
      <h1>{forced ? 'Задайте свій пароль' : 'Зміна пароля'}</h1>
      {forced ? (
        <p>
          Ви увійшли стандартним обліковим записом admin/admin. Поки не задасте власний
          пароль, дашборд не відкриється.
        </p>
      ) : null}
      {done ? (
        <>
          <div className="auth-ok" role="status">
            Пароль змінено. Інші сеанси цього облікового запису завершено.
          </div>
          <div className="auth-actions">
            <button type="button" className="auth-primary" onClick={onClose}>
              Готово
            </button>
          </div>
        </>
      ) : (
        <>
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
            {forced ? (
              onSignOut ? (
                <button type="button" className="auth-secondary" onClick={onSignOut} disabled={busy}>
                  Вийти
                </button>
              ) : null
            ) : (
              <button type="button" className="auth-secondary" onClick={onClose} disabled={busy}>
                Скасувати
              </button>
            )}
            <button type="submit" className="auth-primary" disabled={busy}>
              {busy ? 'Збереження…' : 'Змінити пароль'}
            </button>
          </div>
        </>
      )}
    </form>
  )

  if (forced) return <main className="auth-page">{form}</main>
  return (
    <div className="auth-modal-overlay" role="presentation" onClick={() => !busy && onClose()}>
      {form}
    </div>
  )
}
