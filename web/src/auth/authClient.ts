import { apiError, apiRequest, withBase } from '../api'
import type { AuthMe } from './permissions'

// fetchMe returns the signed-in user, or null without a session. It uses
// apiRequest, not apiFetch: a 401 here is an answer, not an expiry.
export async function fetchMe(signal?: AbortSignal): Promise<AuthMe | null> {
  const res = await apiRequest(withBase('/api/v1/auth/me'), { signal })
  if (res.status === 401) return null
  if (!res.ok) throw await apiError(res, 'auth/me request failed')
  return (await res.json()) as AuthMe
}

export async function login(email: string, password: string): Promise<AuthMe> {
  const res = await apiRequest(withBase('/api/v1/auth/login'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося увійти')
  return (await res.json()) as AuthMe
}

export async function logout(): Promise<void> {
  const res = await apiRequest(withBase('/api/v1/auth/logout'), { method: 'POST' })
  if (!res.ok) throw await apiError(res, 'logout failed')
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const res = await apiRequest(withBase('/api/v1/auth/password'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося змінити пароль')
}
