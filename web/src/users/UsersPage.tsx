import { useEffect, useMemo, useState } from 'react'
import '../alerts/alerts.css'
import { useAuth } from '../auth/authContext'
import { KNOWN_ORGANIZATIONS, formatOrganizationLabel } from '../dashboard/config'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar } from '../shell/ModeTopBar'
import { grantSummary } from './grantScopes'
import { IssuedCredentials } from './IssuedCredentials'
import type { Issued, OrgOption } from './types'
import { UserForm } from './UserForm'
import { fetchUsers, type UserAccount } from './usersClient'
import './users.css'

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
