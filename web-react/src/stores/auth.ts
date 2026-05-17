import { create } from 'zustand'
import { localLogin, ssoComplete } from '@/api/auth'
import type { Role } from '@/api/types'

interface AuthState {
  userId: number | null
  upn: string
  displayName: string
  role: Role | ''
  login: (upn: string, password: string) => Promise<void>
  loginSSO: () => Promise<void>
  setDisplayName: (name: string) => void
  logout: () => void
}

const STORAGE_KEY = 'psp_user'

interface PersistedAuthState {
  userId: number | null
  upn: string
  displayName: string
  role: Role | ''
}

function loadFromStorage(): PersistedAuthState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw) as PersistedAuthState
  } catch { /* ignore */ }
  return { userId: null, upn: '', displayName: '', role: '' }
}

function persist(state: PersistedAuthState) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(state))
}

export const useAuthStore = create<AuthState>((set, get) => ({
  ...loadFromStorage(),

  async login(upn, password) {
    const res = await localLogin(upn, password)
    localStorage.setItem('psp_access', res.access_token)
    localStorage.setItem('psp_refresh', res.refresh_token)
    const next: PersistedAuthState = {
      userId: res.user.id,
      upn: res.user.upn,
      displayName: res.user.display_name || '',
      role: res.user.role,
    }
    set(next)
    persist(next)
  },

  async loginSSO() {
    const res = await ssoComplete()
    localStorage.setItem('psp_access', res.access_token)
    localStorage.setItem('psp_refresh', res.refresh_token)
    const next: PersistedAuthState = {
      userId: res.user.id,
      upn: res.user.upn,
      displayName: res.user.display_name || '',
      role: res.user.role,
    }
    set(next)
    persist(next)
  },

  setDisplayName(name) {
    const { userId, upn, role } = get()
    const next: PersistedAuthState = { userId, upn, displayName: name, role }
    set(next)
    persist(next)
  },

  logout() {
    localStorage.removeItem('psp_access')
    localStorage.removeItem('psp_refresh')
    localStorage.removeItem(STORAGE_KEY)
    set({ userId: null, upn: '', displayName: '', role: '' })
  },
}))

// Selectors for ergonomics.
export const selectIsLoggedIn = () => !!localStorage.getItem('psp_access')
export const selectIsAdmin = (s: AuthState) => s.role === 'admin'
// Operators count as "staff" for nav / route gating: they can see the
// admin SPA but their backend calls are scoped down by per-handler role
// checks. Pure admins should always satisfy isStaff too.
export const selectIsStaff = (s: AuthState) => s.role === 'admin' || s.role === 'operator'
export const selectLabel = (s: AuthState) => s.displayName || s.upn || 'User'
