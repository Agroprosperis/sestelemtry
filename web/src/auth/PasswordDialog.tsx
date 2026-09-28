import { useEffect, useState } from 'react'
import { PasswordForm } from './PasswordForm'
import './auth.css'

// PasswordDialog is the user menu's «Змінити пароль» modal.
export function PasswordDialog({ onClose }: { onClose: () => void }) {
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  return (
    <div className="auth-modal-overlay" role="presentation" onClick={() => !busy && onClose()}>
      {done ? (
        <div
          className="auth-card"
          role="dialog"
          aria-modal
          aria-label="Зміна пароля"
          onClick={(e) => e.stopPropagation()}
        >
          <h1>Зміна пароля</h1>
          <div className="auth-ok" role="status">
            Пароль змінено. Інші сеанси цього облікового запису завершено.
          </div>
          <div className="auth-actions">
            <button type="button" className="auth-primary" onClick={onClose}>
              Готово
            </button>
          </div>
        </div>
      ) : (
        <PasswordForm
          dialog
          secondary={{ label: 'Скасувати', onClick: onClose }}
          onChanged={() => setDone(true)}
          onBusyChange={setBusy}
        >
          <h1>Зміна пароля</h1>
        </PasswordForm>
      )}
    </div>
  )
}
