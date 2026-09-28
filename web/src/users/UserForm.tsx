import { useId, useState, type FormEvent } from 'react'
import { MIN_PASSWORD_LEN, ROLES, type Role } from '../auth/permissions'
import { grantsFromScopes, scopesFromGrants, type RoleScope, type RoleScopes } from './grantScopes'
import { generatePassword } from './password'
import type { Issued, OrgOption } from './types'
import { createUser, updateUser, type UserAccount } from './usersClient'

export function UserForm({
  user,
  self,
  organizations,
  onCancel,
  onSaved,
}: {
  user: UserAccount | null
  self: boolean
  organizations: OrgOption[]
  onCancel: () => void
  // handedOut is set when the save gave someone a temporary password.
  onSaved: (handedOut?: Issued) => void
}) {
  const passwordLabel = useId()
  const [email, setEmail] = useState(user?.email ?? '')
  const [name, setName] = useState(user?.name ?? '')
  // A new account starts with a generated password the administrator
  // can keep, regenerate or replace.
  const [password, setPassword] = useState(() => (user === null ? generatePassword() : ''))
  const [disabled, setDisabled] = useState(user?.disabled ?? false)
  const [scopes, setScopes] = useState<RoleScopes>(() => scopesFromGrants(user?.grants ?? []))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const setScope = (role: Role, patch: Partial<RoleScope>) =>
    setScopes((prev) => ({ ...prev, [role]: { ...prev[role], ...patch } }))

  const toggleOrg = (role: Role, org: string, on: boolean) =>
    setScopes((prev) => {
      const current = prev[role].organizations.filter((id) => id !== org)
      return { ...prev, [role]: { ...prev[role], organizations: on ? [...current, org] : current } }
    })

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const empty = ROLES.find((r) => scopes[r.id].mode === 'some' && scopes[r.id].organizations.length === 0)
    if (empty) {
      setError(`Оберіть обʼєкти для ролі «${empty.label}» або змініть її обсяг`)
      return
    }
    if ((user === null || password !== '') && [...password].length < MIN_PASSWORD_LEN) {
      setError(`Пароль має містити щонайменше ${MIN_PASSWORD_LEN} символів`)
      return
    }
    setBusy(true)
    setError(null)
    try {
      const grants = grantsFromScopes(scopes)
      if (user === null) {
        const saved = await createUser({ email, name, password, grants })
        onSaved({ email: saved.email, password, created: true })
      } else {
        await updateUser(user.id, { name, disabled, grants, ...(password ? { password } : {}) })
        // Your own new password is not handed out, and the change signs
        // you out anyway.
        onSaved(password && !self ? { email: user.email, password, created: false } : undefined)
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Не вдалося зберегти користувача')
      setBusy(false)
    }
  }

  const noRoles = ROLES.every((r) => scopes[r.id].mode === 'none')
  const known = new Set(organizations.map((o) => o.id))
  // A grant can outlive its object in the config; listing it lets the
  // administrator take it off (the API rejects saving it again).
  const choicesFor = (scope: RoleScope): OrgOption[] => [
    ...organizations,
    ...scope.organizations
      .filter((id) => !known.has(id))
      .map((id) => ({ id, name: `${id} — немає в конфігурації` })),
  ]

  return (
    <form className="alerts-card" onSubmit={(e) => void submit(e)}>
      <span className="alerts-card-accent" />
      <div className="alerts-card-head">
        <h2 className="alerts-section-title">{user ? 'Зміна користувача' : 'Новий користувач'}</h2>
      </div>

      <div className="alerts-grid">
        <label className="alerts-field">
          <span>Email</span>
          <input
            className="alerts-input"
            type="email"
            required
            disabled={user !== null}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
        <label className="alerts-field">
          <span>Імʼя</span>
          <input className="alerts-input" value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <div className="alerts-field alerts-field-wide">
          <span id={passwordLabel}>
            {self ? 'Новий пароль' : user ? 'Новий тимчасовий пароль' : 'Тимчасовий пароль'}
          </span>
          <div className="users-password">
            {/* Shown in clear: it is meant to be passed on, and a password
                field would make the browser offer to save it as yours. */}
            <input
              className="alerts-input users-password-input"
              type="text"
              autoComplete="off"
              spellCheck={false}
              aria-labelledby={passwordLabel}
              required={user === null}
              placeholder={user ? 'порожньо — без змін' : `щонайменше ${MIN_PASSWORD_LEN} символів`}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <button type="button" className="alerts-secondary" onClick={() => setPassword(generatePassword())}>
              Згенерувати
            </button>
          </div>
          {!self ? <small className="users-hint">При першому вході користувач задасть власний пароль.</small> : null}
        </div>
      </div>

      {user ? (
        <label className="alerts-checkbox">
          <input type="checkbox" checked={disabled} onChange={(e) => setDisabled(e.target.checked)} />
          <span>Обліковий запис вимкнено</span>
        </label>
      ) : null}

      <fieldset className="users-roles">
        <legend>Ролі та обʼєкти</legend>
        {ROLES.map((role) => {
          const scope = scopes[role.id]
          return (
            <div className="users-role" key={role.id}>
              <div className="users-role-head">
                <strong>{role.label}</strong>
                <span>{role.hint}</span>
                <select
                  className="alerts-input"
                  aria-label={`Обсяг ролі «${role.label}»`}
                  value={scope.mode}
                  onChange={(e) => setScope(role.id, { mode: e.target.value as RoleScope['mode'] })}
                >
                  <option value="none">не призначено</option>
                  <option value="all">усі обʼєкти</option>
                  <option value="some">вибрані обʼєкти</option>
                </select>
              </div>
              {scope.mode === 'some' ? (
                <div className="users-role-orgs">
                  {choicesFor(scope).map((org) => (
                    <label className="alerts-checkbox" key={org.id}>
                      <input
                        type="checkbox"
                        checked={scope.organizations.includes(org.id)}
                        onChange={(e) => toggleOrg(role.id, org.id, e.target.checked)}
                      />
                      <span>{org.name}</span>
                    </label>
                  ))}
                </div>
              ) : null}
            </div>
          )
        })}
      </fieldset>

      {noRoles ? (
        <p className="alerts-section-sub">Без ролей користувач після входу не побачить жодного розділу.</p>
      ) : null}
      {self ? (
        <p className="alerts-section-sub">
          Це ваш обліковий запис: після зміни ролей чи пароля доведеться увійти знову.
        </p>
      ) : null}
      {error ? <div className="alerts-banner alerts-banner-error">{error}</div> : null}

      <div className="alerts-actions">
        <button type="button" className="alerts-secondary" onClick={onCancel} disabled={busy}>
          Скасувати
        </button>
        <button type="submit" className="alerts-save" disabled={busy}>
          {busy ? 'Збереження…' : user ? 'Зберегти' : 'Створити'}
        </button>
      </div>
    </form>
  )
}
