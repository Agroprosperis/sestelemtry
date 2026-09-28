import { PasswordForm } from './PasswordForm'
import './auth.css'

// ForcedPasswordPage stands in for the whole app while the account has
// a temporary password (admin/admin, or one an administrator handed
// out): the only ways past it are a new password or signing out.
export function ForcedPasswordPage({ onChanged, onSignOut }: { onChanged: () => void; onSignOut: () => void }) {
  return (
    <main className="auth-page">
      <PasswordForm secondary={{ label: 'Вийти', onClick: onSignOut }} onChanged={onChanged}>
        <img src="/logo_agroprosperis.png" alt="Агропросперіс" className="auth-logo" />
        <h1>Задайте свій пароль</h1>
        <p>Ви увійшли з тимчасовим паролем. Поки не задасте власний, дашборд не відкриється.</p>
      </PasswordForm>
    </main>
  )
}
