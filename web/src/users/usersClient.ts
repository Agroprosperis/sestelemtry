import { apiError, apiFetch, buildURL, withBase } from '../api'
import type { AuthGrant } from '../auth/permissions'

export type UserAccount = {
  id: number
  email: string
  name: string
  disabled: boolean
  auth_provider: string
  // Still on the temporary password it was given.
  must_change_password: boolean
  grants: AuthGrant[]
  created_at: string
}

export type UserPatch = {
  name?: string
  disabled?: boolean
  // Empty or omitted keeps the current password.
  password?: string
  grants?: AuthGrant[]
}

export async function fetchUsers(signal?: AbortSignal): Promise<UserAccount[]> {
  const res = await apiFetch(withBase('/api/v1/users'), { signal })
  if (!res.ok) throw await apiError(res, 'users request failed')
  const body = (await res.json()) as { users: UserAccount[] }
  return body.users ?? []
}

export async function createUser(input: {
  email: string
  name: string
  password: string
  grants: AuthGrant[]
}): Promise<UserAccount> {
  const res = await apiFetch(withBase('/api/v1/users'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося створити користувача')
  return (await res.json()) as UserAccount
}

export async function updateUser(id: number, patch: UserPatch): Promise<UserAccount> {
  const res = await apiFetch(buildURL('/api/v1/users', { user_id: String(id) }), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося зберегти користувача')
  return (await res.json()) as UserAccount
}
