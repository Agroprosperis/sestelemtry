import { UserCircle } from '@phosphor-icons/react'
import { useEffect, useRef, useState } from 'react'
import { useAuth } from './authContext'
import { PasswordDialog } from './PasswordDialog'
import './auth.css'

function openView(view: string) {
  const url = new URL(window.location.href)
  url.searchParams.set('view', view)
  url.searchParams.delete('tab')
  window.history.pushState({}, '', url)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

// UserMenu is the signed-in user's dropdown in the top bar: who is
// signed in, user management for administrators of every organization,
// password change and sign-out. Renders nothing outside the signed-in app.
export function UserMenu() {
  const auth = useAuth()
  const [open, setOpen] = useState(false)
  const [passwordOpen, setPasswordOpen] = useState(false)
  const [signingOut, setSigningOut] = useState(false)
  const ref = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (!auth) return null
  const { me, logout } = auth
  const display = me.user.name || me.user.email

  return (
    <div className="ctl-topbar-menu" ref={ref}>
      <button
        type="button"
        className="ctl-topbar-menu-btn auth-user-btn"
        aria-haspopup="menu"
        aria-expanded={open}
        title={me.user.email}
        onClick={() => setOpen((v) => !v)}
      >
        <UserCircle size={16} aria-hidden="true" />
        <span>{display}</span>
      </button>
      {open && (
        <div className="ctl-topbar-menu-list" role="menu">
          <div className="auth-user-head">
            <strong>{display}</strong>
            {me.user.name ? me.user.email : null}
          </div>
          {me.global_admin && (
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false)
                openView('users')
              }}
            >
              Користувачі
            </button>
          )}
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false)
              setPasswordOpen(true)
            }}
          >
            Змінити пароль
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={signingOut}
            onClick={() => {
              setSigningOut(true)
              void logout()
            }}
          >
            Вийти
          </button>
        </div>
      )}
      {passwordOpen && <PasswordDialog onClose={() => setPasswordOpen(false)} />}
    </div>
  )
}
