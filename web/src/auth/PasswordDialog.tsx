import { useEffect, useState, type FormEvent } from 'react'
import { changePassword } from './authClient'
import './auth.css'

const MIN_PASSWORD_LEN = 10

export function PasswordDialog({ onClose }: { onClose: () => void }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [busy, onClose])

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
      setDone(true)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Не вдалося змінити пароль')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="auth-modal-overlay" role="presentation" onClick={() => !busy && onClose()}>
      <form
        className="auth-card"
        role="dialog"
        aria-modal="true"
        aria-label="Зміна пароля"
        onClick={(e) => e.stopPropagation()}
        onSubmit={(e) => void submit(e)}
      >
        <h1>Зміна пароля</h1>
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
              <button type="button" className="auth-secondary" onClick={onClose} disabled={busy}>
                Скасувати
              </button>
              <button type="submit" className="auth-primary" disabled={busy}>
                {busy ? 'Збереження…' : 'Змінити пароль'}
              </button>
            </div>
          </>
        )}
      </form>
    </div>
  )
}
