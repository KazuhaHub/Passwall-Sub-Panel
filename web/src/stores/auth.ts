import { defineStore } from 'pinia'
import { localLogin } from '@/api/auth'
import type { Role, UserSource } from '@/api/types'

interface AuthState {
  userId: number | null
  username: string
  role: Role | ''
  source: UserSource | ''
}

const STORAGE_KEY = 'psp_user'

function loadFromStorage(): AuthState {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw)
  } catch {
    // ignored
  }
  return { userId: null, username: '', role: '', source: '' }
}

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => loadFromStorage(),
  getters: {
    loggedIn: (s) => !!sessionStorage.getItem('psp_access'),
    isAdmin: (s) => s.role === 'admin',
  },
  actions: {
    async login(username: string, password: string) {
      const res = await localLogin(username, password)
      sessionStorage.setItem('psp_access', res.access_token)
      sessionStorage.setItem('psp_refresh', res.refresh_token)
      this.userId = res.user.id
      this.username = res.user.username
      this.role = res.user.role
      this.source = res.user.source
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify(this.$state))
    },
    logout() {
      sessionStorage.removeItem('psp_access')
      sessionStorage.removeItem('psp_refresh')
      sessionStorage.removeItem(STORAGE_KEY)
      this.userId = null
      this.username = ''
      this.role = ''
      this.source = ''
    },
  },
})
