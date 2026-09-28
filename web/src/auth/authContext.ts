import { createContext, useContext } from 'react'
import { UNRESTRICTED, type Access, type AuthMe } from './permissions'

export type AuthContextValue = {
  me: AuthMe
  access: Access
  logout: () => Promise<void>
}

export const AuthContext = createContext<AuthContextValue | null>(null)

// useAuth is null outside the signed-in app (component tests).
export function useAuth(): AuthContextValue | null {
  return useContext(AuthContext)
}

export function useAccess(): Access {
  return useContext(AuthContext)?.access ?? UNRESTRICTED
}
