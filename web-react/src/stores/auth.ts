import { create } from 'zustand'
import { localLogin, ssoComplete } from '@/api/auth'
import type { Role } from '@/api/types'

interface AuthState {
  userId: number | null
  upn: string
  displayName: string
  role: Role | ''
  // hasToken mirrors `psp_access` presence so route guards / Header
  // components can subscribe via Zustand instead of poking localStorage
  // directly (which doesn't trigger re-renders). Kept in sync by
  // login / loginSSO / logout / the cross-tab storage listener.
  hasToken: boolean
  login: (upn: string, password: string) => Promise<void>
  loginSSO: () => Promise<void>
  setDisplayName: (name: string) => void
  logout: () => void
  syncFromStorage: () => void
}

const STORAGE_KEY = 'psp_user'
const ACCESS_KEY = 'psp_access'

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
  hasToken: !!localStorage.getItem(ACCESS_KEY),

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
    set({ ...next, hasToken: true })
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
    set({ ...next, hasToken: true })
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
    set({ userId: null, upn: '', displayName: '', role: '', hasToken: false })
    window.location.replace('/logged-out')
  },

  // syncFromStorage rebuilds the in-memory state from localStorage —
  // called by the cross-tab `storage` event listener so a logout in
  // one tab knocks every other tab out of admin / user routes on the
  // next render, instead of waiting for the next API call to 401.
  syncFromStorage() {
    const persisted = loadFromStorage()
    set({ ...persisted, hasToken: !!localStorage.getItem(ACCESS_KEY) })
  },
}))

// Cross-tab logout: when another tab clears psp_access the `storage`
// event fires here. Zustand's set() then triggers a re-render of every
// selector — RequireAuth re-evaluates and bounces the user out.
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (e) => {
    if (e.key === ACCESS_KEY || e.key === STORAGE_KEY) {
      useAuthStore.getState().syncFromStorage()
    }
  })
}

// Selectors for ergonomics. selectIsLoggedIn now reads the reactive
// hasToken field so it integrates with the store's subscription
// machinery; the previous direct-localStorage read worked for the
// initial mount but missed updates from cross-tab logouts.
export const selectIsLoggedIn = (s: AuthState) => s.hasToken
export const selectIsAdmin = (s: AuthState) => s.role === 'admin'
// Operators count as "staff" for nav / route gating: they can see the
// admin SPA but their backend calls are scoped down by per-handler role
// checks. Pure admins should always satisfy isStaff too.
export const selectIsStaff = (s: AuthState) => s.role === 'admin' || s.role === 'operator'
export const selectLabel = (s: AuthState) => s.displayName || s.upn || 'User'
