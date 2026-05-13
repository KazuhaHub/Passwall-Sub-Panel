import { defineStore } from 'pinia'
import { localLogin, ssoComplete } from '@/api/auth'
import type { Role } from '@/api/types'

interface AuthState {
  userId: number | null
  upn: string
  displayName: string
  role: Role | ''
}

const STORAGE_KEY = 'psp_user'

function loadFromStorage(): AuthState {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw)
  } catch {
    // ignored
  }
  return { userId: null, upn: '', displayName: '', role: '' }
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => loadFromStorage(),
  getters: {
    loggedIn: (s) => !!sessionStorage.getItem('psp_access'),
    isAdmin: (s) => s.role === 'admin',
    label: (s) => s.displayName || s.upn || 'User',
  },
  actions: {
    async login(upn: string, password: string) {
      const res = await localLogin(upn, password)
      sessionStorage.setItem('psp_access', res.access_token)
      sessionStorage.setItem('psp_refresh', res.refresh_token)
      this.userId = res.user.id
      this.upn = res.user.upn
      this.displayName = res.user.display_name || ''
      this.role = res.user.role
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify(this.$state))
    },
    async loginSSO() {
      const res = await ssoComplete()
      sessionStorage.setItem('psp_access', res.access_token)
      sessionStorage.setItem('psp_refresh', res.refresh_token)
      this.userId = res.user.id
      this.upn = res.user.upn
      this.displayName = res.user.display_name || ''
      this.role = res.user.role
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify(this.$state))
    },
    // setDisplayName updates the cached display name without re-issuing
    // tokens. Use after the admin edits their own profile, so the top-bar
    // label updates in place instead of waiting for the next login.
    setDisplayName(name: string) {
      this.displayName = name
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify(this.$state))
    },
    logout() {
      sessionStorage.removeItem('psp_access')
      sessionStorage.removeItem('psp_refresh')
      sessionStorage.removeItem(STORAGE_KEY)
      this.userId = null
      this.upn = ''
      this.displayName = ''
      this.role = ''
    },
  },
})
