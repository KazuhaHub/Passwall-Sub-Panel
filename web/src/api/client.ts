import axios from 'axios'
import { ElMessage } from 'element-plus'

// Shared axios instance. Token is attached automatically from session
// storage; on 401 the user is bounced to /login.
export const client = axios.create({
  baseURL: '/api',
  timeout: 30000,
})

client.interceptors.request.use((config) => {
  const token = sessionStorage.getItem('psp_access')
  if (token && config.headers) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Throttle the "3X-UI sync pending" toast so a burst of admin clicks
// doesn't spam the user with the same warning.
let lastSyncPendingToast = 0

function responseErrorMessage(err: any): string {
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
        ElMessage.warning('3X-UI 暂时不可达，同步任务已加入后台队列（每分钟自动重试）')
      }
    }
    return res
  },
  (err) => {
    if (err.response?.status === 401) {
      sessionStorage.removeItem('psp_access')
      sessionStorage.removeItem('psp_refresh')
      const onLoginPage = location.pathname === '/login' || location.pathname.startsWith('/login/')
      if (onLoginPage) {
        ElMessage.error(responseErrorMessage(err))
      } else {
        location.href = '/login'
      }
    } else if (err.response?.data?.error) {
      ElMessage.error(responseErrorMessage(err))
    } else {
      ElMessage.error(responseErrorMessage(err))
    }
    return Promise.reject(err)
  },
)
