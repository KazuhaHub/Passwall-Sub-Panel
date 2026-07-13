import { create } from 'zustand'
import { panelURL } from '@/panelPath'
import { startAuthentication } from '@simplewebauthn/browser'
import {
  localLogin,
  passkey2FABegin,
  passkey2FAFinish,
  passkeyLoginBegin,
  passkeyLoginFinish,
  ssoComplete,
  verify2FA,
} from '@/api/auth'
import { isTwoFAChallenge } from '@/api/types'
import type { AuthLoginResponse, LoginCaptcha, Role, TwoFAMethod } from '@/api/types'

// LoginOutcome tells the login form whether the credentials produced a session
// or a 2FA challenge that must be completed. `methods` is the set of alternative
// verification methods the server will accept for this challenge.
export type LoginOutcome =
  | { twoFA: false }
  | { twoFA: true; pendingToken: string; methods: TwoFAMethod[] }

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
  login: (upn: string, password: string, captcha?: LoginCaptcha) => Promise<LoginOutcome>
  // loginPasskey runs a usernameless WebAuthn login. Same outcome contract as
  // login() — it may surface a 2FA challenge for accounts that also enrolled TOTP.
  loginPasskey: () => Promise<LoginOutcome>
  // complete2FA finishes a login that returned a 2FA challenge: it exchanges the
  // pending token + code (TOTP, recovery, or emailed code) for a real session.
  complete2FA: (pendingToken: string, code: string) => Promise<void>
  // complete2FAPasskey finishes a 2FA challenge by asserting a passkey instead of
  // a code (only offered when the first factor was a password).
  complete2FAPasskey: (pendingToken: string) => Promise<void>
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

// applySession writes the tokens + user identity from a full login response into
// localStorage and the store. Shared by login / complete2FA / loginSSO.
function applySession(res: AuthLoginResponse, set: (partial: Partial<AuthState>) => void) {
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
}

export const useAuthStore = create<AuthState>((set, get) => ({
  ...loadFromStorage(),
  hasToken: !!localStorage.getItem(ACCESS_KEY),

  async login(upn, password, captcha) {
    const res = await localLogin(upn, password, captcha)
    // A 2FA-enabled account returns a challenge, not a session: hold the
    // pending token and let the form collect the second factor.
    if (isTwoFAChallenge(res)) {
      return { twoFA: true, pendingToken: res.pending_token, methods: res.methods ?? ['totp', 'recovery'] }
    }
    applySession(res, set)
    return { twoFA: false }
  },

  async loginPasskey() {
    const { session_id, publicKey } = await passkeyLoginBegin()
    // The browser WebAuthn ceremony runs outside React/axios.
    const assertion = await startAuthentication({ optionsJSON: publicKey })
    const res = await passkeyLoginFinish(session_id, assertion)
    if (isTwoFAChallenge(res)) {
      return { twoFA: true, pendingToken: res.pending_token, methods: res.methods ?? ['totp', 'recovery'] }
    }
    applySession(res, set)
    return { twoFA: false }
  },

  async complete2FA(pendingToken, code) {
    const res = await verify2FA(pendingToken, code)
    applySession(res, set)
  },

  async complete2FAPasskey(pendingToken) {
    const { session_id, publicKey } = await passkey2FABegin(pendingToken)
    const assertion = await startAuthentication({ optionsJSON: publicKey })
    const res = await passkey2FAFinish(pendingToken, session_id, assertion)
    applySession(res, set)
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
    window.location.replace(panelURL('/logged-out'))
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
