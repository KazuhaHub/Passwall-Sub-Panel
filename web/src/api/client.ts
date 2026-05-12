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

client.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response?.status === 401) {
      sessionStorage.removeItem('psp_access')
      sessionStorage.removeItem('psp_refresh')
      if (location.pathname !== '/login') {
        location.href = '/login'
      }
    } else if (err.response?.data?.error) {
      ElMessage.error(err.response.data.error)
    } else {
      ElMessage.error(err.message || 'request failed')
    }
    return Promise.reject(err)
  },
)
