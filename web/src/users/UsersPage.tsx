import { useCallback, useEffect, useMemo, useState } from 'react'
import '../alerts/alerts.css'
import { useAuth } from '../auth/authContext'
import { ROLES, type Role } from '../auth/permissions'
import { KNOWN_ORGANIZATIONS, formatOrganizationLabel } from '../dashboard/config'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar } from '../shell/ModeTopBar'
import { grantSummary } from './grantScopes'
import { IssuedCredentials } from './IssuedCredentials'
import type { Issued, OrgOption } from './types'
import { UserForm } from './UserForm'
import { fetchUsers, type UserAccount } from './usersClient'
import './users.css'

type RoleFilter = 'all' | 'none' | Role
type SortKey = 'name' | 'role'
type SortDir = 'asc' | 'desc'

const UK_COLLATOR = new Intl.Collator('uk', { sensitivity: 'base', numeric: true })
const ROLE_ORDER = new Map<Role, number>(ROLES.map((r, i) => [r.id, i]))

function surnameKey(u: UserAccount): string {
  const name = u.name.trim()
  if (!name) return u.email
  return name
}

function primaryRoleRank(u: UserAccount): number {
  let best = ROLES.length
  for (const g of u.grants) {
    const idx = ROLE_ORDER.get(g.role)
    if (idx !== undefined && idx < best) best = idx
  }
  return best
}

function compareUsers(
  a: UserAccount,
  b: UserAccount,
  sortKey: SortKey,
  sortDir: SortDir,
  orgName: (id: string) => string,
): number {
  const sign = sortDir === 'asc' ? 1 : -1
  if (sortKey === 'role') {
    const rankDiff = primaryRoleRank(a) - primaryRoleRank(b)
    if (rankDiff !== 0) return rankDiff * sign
    const roleDiff = UK_COLLATOR.compare(
      grantSummary(a.grants, orgName).join('; '),
      grantSummary(b.grants, orgName).join('; '),
    )
    if (roleDiff !== 0) return roleDiff * sign
  }
  const nameDiff = UK_COLLATOR.compare(surnameKey(a), surnameKey(b))
  if (nameDiff !== 0) return nameDiff * sign
  return UK_COLLATOR.compare(a.email, b.email) * sign
}

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
  const orgName = useCallback(
    (id: string) => organizations.find((o) => o.id === id)?.name ?? id,
    [organizations],
  )

  const [users, setUsers] = useState<UserAccount[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState<UserAccount | 'new' | null>(null)
  const [issued, setIssued] = useState<Issued | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [search, setSearch] = useState('')
  const [roleFilter, setRoleFilter] = useState<RoleFilter>('all')
  const [sortKey, setSortKey] = useState<SortKey>('name')
  const [sortDir, setSortDir] = useState<SortDir>('asc')

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const visibleUsers = useMemo(() => {
    if (!users) return null
    const q = search.trim().toLocaleLowerCase('uk')
    return users
      .filter((u) => {
        if (roleFilter === 'none') {
          if (u.grants.length > 0) return false
        } else if (roleFilter !== 'all') {
          if (!u.grants.some((g) => g.role === roleFilter)) return false
        }
        if (!q) return true
        return (
          u.name.toLocaleLowerCase('uk').includes(q) ||
          u.email.toLocaleLowerCase('uk').includes(q)
        )
      })
      .sort((a, b) => compareUsers(a, b, sortKey, sortDir, orgName))
  }, [users, search, roleFilter, sortKey, sortDir, orgName])

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
          onDeleted={() => {
            setEditing(null)
            setIssued(null)
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
          <h2 className="alerts-section-title">
            Облікові записи
            {users !== null && visibleUsers !== null ? (
              <span className="users-count">
                {visibleUsers.length === users.length
                  ? `${users.length}`
                  : `${visibleUsers.length} з ${users.length}`}
              </span>
            ) : null}
          </h2>
          {users !== null && users.length > 0 ? (
            <div className="users-filters">
              <input
                type="search"
                className="alerts-input users-search"
                placeholder="Пошук за прізвищем або email…"
                aria-label="Пошук користувача"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
              <select
                className="alerts-input users-role-filter"
                aria-label="Фільтр за роллю"
                value={roleFilter}
                onChange={(e) => setRoleFilter(e.target.value as RoleFilter)}
              >
                <option value="all">Усі ролі</option>
                {ROLES.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.label}
                  </option>
                ))}
                <option value="none">Без ролей</option>
              </select>
            </div>
          ) : null}
        </div>
        {visibleUsers === null ? (
          error ? null : <p className="alerts-muted">Завантаження…</p>
        ) : visibleUsers.length === 0 ? (
          <p className="alerts-muted">Нікого не знайдено за вибраними умовами.</p>
        ) : (
          <table className="alerts-orgs users-table">
            <thead>
              <tr>
                <th aria-sort={sortKey === 'name' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'}>
                  <button
                    type="button"
                    className={`users-sort-btn${sortKey === 'name' ? ' is-active' : ''}`}
                    onClick={() => toggleSort('name')}
                  >
                    <span>Користувач</span>
                    <span className="users-sort-arrow" aria-hidden="true">
                      {sortKey === 'name' ? (sortDir === 'asc' ? '↑' : '↓') : '↕'}
                    </span>
                  </button>
                </th>
                <th aria-sort={sortKey === 'role' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'}>
                  <button
                    type="button"
                    className={`users-sort-btn${sortKey === 'role' ? ' is-active' : ''}`}
                    onClick={() => toggleSort('role')}
                  >
                    <span>Ролі</span>
                    <span className="users-sort-arrow" aria-hidden="true">
                      {sortKey === 'role' ? (sortDir === 'asc' ? '↑' : '↓') : '↕'}
                    </span>
                  </button>
                </th>
                <th>Стан</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {visibleUsers.map((u) => {
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
