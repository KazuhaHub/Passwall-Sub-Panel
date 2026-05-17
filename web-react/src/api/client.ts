import axios, { AxiosError } from 'axios'
import i18n from '@/i18n'
import { pushSnack } from '@/components/SnackbarHost'

// Shared axios instance. Bearer token is attached automatically from
// session storage; on 401 the user is bounced to /login.
export const client = axios.create({
  baseURL: '/api',
  timeout: 30000,
})

client.interceptors.request.use((config) => {
  const token = localStorage.getItem('psp_access')
  if (token && config.headers) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Throttle the "3X-UI sync pending" toast so a burst of admin clicks
// doesn't spam the user with the same warning.
let lastSyncPendingToast = 0

function responseErrorMessage(err: AxiosError<{ error?: string }>): string {
  return err.response?.data?.error || err.message || 'request failed'
}

client.interceptors.response.use(
  (res) => {
    // Backend signals "operation succeeded synchronously on the panel
    // side, but 3X-UI sync had to be queued for background retry" via the
    // X-Sync-Pending response header. Surface that here so the admin
    // knows changes won't reach 3X-UI until the panel can reach it.
    if (res.headers?.['x-sync-pending'] === '1') {
      const now = Date.now()
      if (now - lastSyncPendingToast > 3000) {
        lastSyncPendingToast = now
        pushSnack(i18n.t('common:errors.sync_pending'), 'warning')
      }
    }
    return res
  },
  (err: AxiosError<{ error?: string }>) => {
    if (err.response?.status === 401) {
      localStorage.removeItem('psp_access')
      localStorage.removeItem('psp_refresh')
      localStorage.removeItem('psp_user')
      const onLoginPage = location.pathname === '/login' || location.pathname.startsWith('/login/')
      if (onLoginPage) {
        pushSnack(responseErrorMessage(err), 'error')
      } else {
        location.href = '/login'
      }
    } else {
      pushSnack(responseErrorMessage(err), 'error')
    }
    return Promise.reject(err)
  },
)
