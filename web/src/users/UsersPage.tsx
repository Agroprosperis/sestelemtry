import { useEffect, useId, useMemo, useState, type FormEvent } from 'react'
import '../alerts/alerts.css'
import { useAuth } from '../auth/authContext'
import { ROLES, type Role } from '../auth/permissions'
import { KNOWN_ORGANIZATIONS, formatOrganizationLabel } from '../dashboard/config'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar } from '../shell/ModeTopBar'
import {
  grantSummary,
  grantsFromScopes,
  scopesFromGrants,
  type RoleScope,
  type RoleScopes,
} from './grantScopes'
import { copyText, generatePassword } from './password'
import { createUser, fetchUsers, updateUser, type UserAccount } from './usersClient'
import './users.css'

const MIN_PASSWORD_LEN = 10

type OrgOption = { id: string; name: string }

// Issued is a temporary password just handed out, shown once so the
// administrator can pass it on.
type Issued = { email: string; password: string; created: boolean }

export function UsersPage() {
  const { organizationID, options, change } = useOrganizationParam('service')
  const auth = useAuth()
  const organizations = useMemo<OrgOption[]>(
    () =>
      auth
        ? auth.me.organizations.map((o) => ({ id: o.id, name: o.name || formatOrganizationLabel(o.id) }))
        : KNOWN_ORGANIZATIONS.map((id) => ({ id, name: formatOrganizationLabel(id) })),
    [auth],
  )
  const orgName = (id: string) => organizations.find((o) => o.id === id)?.name ?? id

  const [users, setUsers] = useState<UserAccount[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<UserAccount | 'new' | null>(null)
  const [issued, setIssued] = useState<Issued | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    const ac = new AbortController()
    fetchUsers(ac.signal)
      .then((list) => {
        setUsers(list)
        setError(null)
      })
      .catch((e: unknown) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        setError(e instanceof Error ? e.message : 'Не вдалося завантажити користувачів')
      })
    return () => ac.abort()
  }, [reloadKey])

  return (
    <main className="alerts-page">
      <ModeTopBar
        mode="none"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={change}
        title="Користувачі"
      />

      <p className="alerts-subtitle">
        Роль призначається на окремі обʼєкти або на всі — «усі обʼєкти» охоплює й ті, що
        зʼявляться в конфігурації пізніше. Після зміни ролей, пароля чи вимкнення користувач
        входить знову.
      </p>

      {error ? <div className="alerts-banner alerts-banner-error">{error}</div> : null}
      {organizations.length === 0 ? (
        <div className="alerts-banner alerts-banner-info">
          API не повернуло жодного обʼєкта: запустіть його з <code>-config</code>. Доти ролі
          можна призначати лише на всі обʼєкти.
        </div>
      ) : null}

      {issued ? <IssuedCredentials issued={issued} onClose={() => setIssued(null)} /> : null}

      {editing ? (
        <UserForm
          key={editing === 'new' ? 'new' : editing.id}
          user={editing === 'new' ? null : editing}
          self={editing !== 'new' && auth?.me.user.id === editing.id}
          organizations={organizations}
          onCancel={() => setEditing(null)}
          onSaved={(handedOut) => {
            setEditing(null)
            setIssued(handedOut ?? null)
            setReloadKey((k) => k + 1)
          }}
        />
      ) : (
        <div className="users-toolbar">
          <button
            type="button"
            className="alerts-save"
            onClick={() => {
              setIssued(null)
              setEditing('new')
            }}
          >
            Новий користувач
          </button>
        </div>
      )}

      <section className="alerts-card">
        <span className="alerts-card-accent alerts-card-accent-violet" />
        <div className="alerts-card-head">
          <h2 className="alerts-section-title">Облікові записи</h2>
        </div>
        {users === null ? (
          error ? null : <p className="alerts-muted">Завантаження…</p>
        ) : (
          <table className="alerts-orgs users-table">
            <thead>
              <tr>
                <th>Користувач</th>
                <th>Ролі</th>
                <th>Стан</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const lines = grantSummary(u.grants, orgName)
                return (
                  <tr key={u.id}>
                    <td>
                      <span className="alerts-org-name">{u.name || u.email}</span>
                      {u.name ? <span className="alerts-org-id">{u.email}</span> : null}
                    </td>
                    <td>
                      {lines.length > 0 ? (
                        <ul className="users-grants">
                          {lines.map((line) => (
                            <li key={line}>{line}</li>
                          ))}
                        </ul>
                      ) : (
                        <span className="users-muted">без ролей</span>
                      )}
                    </td>
                    <td>
                      {u.disabled ? (
                        <span className="users-status-off">вимкнено</span>
                      ) : u.must_change_password ? (
                        <span className="users-status-pending">ще не задав свій пароль</span>
                      ) : (
                        'активний'
                      )}
                    </td>
                    <td>
                      <button type="button" className="alerts-secondary" onClick={() => setEditing(u)}>
                        Змінити
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </section>
    </main>
  )
}

function IssuedCredentials({ issued, onClose }: { issued: Issued; onClose: () => void }) {
  const [copied, setCopied] = useState<boolean | null>(null)
  const address = window.location.origin
  const text = `Адреса: ${address}\nЛогін: ${issued.email}\nТимчасовий пароль: ${issued.password}`
  return (
    <section className="alerts-card" role="status">
      <span className="alerts-card-accent" />
      <div className="alerts-card-head">
        <h2 className="alerts-section-title">{issued.created ? 'Користувача створено' : 'Пароль змінено'}</h2>
      </div>
      <p className="alerts-section-sub">
        Передайте ці дані користувачу. При першому вході дашборд попросить задати власний
        пароль; цей більше ніде не показується.
      </p>
      <dl className="users-credentials">
        <dt>Адреса</dt>
        <dd>
          <code>{address}</code>
        </dd>
        <dt>Логін</dt>
        <dd>
          <code>{issued.email}</code>
        </dd>
        <dt>Пароль</dt>
        <dd>
          <code>{issued.password}</code>
        </dd>
      </dl>
      <div className="alerts-actions">
        {copied === false ? (
          <span className="alerts-dirty-note">Не вдалося скопіювати — виділіть дані й скопіюйте вручну.</span>
        ) : null}
        <button type="button" className="alerts-secondary" onClick={() => void copyText(text).then(setCopied)}>
          {copied ? 'Скопійовано' : 'Копіювати'}
        </button>
        <button type="button" className="alerts-save" onClick={onClose}>
          Готово
        </button>
      </div>
    </section>
  )
}

function UserForm({
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
