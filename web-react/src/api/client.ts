import axios, { AxiosError, AxiosRequestConfig, InternalAxiosRequestConfig } from 'axios'
import i18n from '@/i18n'
import { pushSnack } from '@/components/SnackbarHost'
import { panelAPIBase, panelURL } from '@/panelPath'

// Shared axios instance. Bearer token is attached automatically from
// local storage. The response interceptor centralises three concerns:
//   1. silent access-token refresh on 401 (replays the original request
//      once with the new token instead of bouncing to /login)
//   2. categorised error toast (network vs timeout vs server vs client)
//   3. de-duplication of the same error fired N times in a tight burst
//      — e.g. Promise.allSettled fan-out across a user-batch
export const client = axios.create({
  baseURL: panelAPIBase,
  timeout: 30000,
})

// Optional per-request flags. Set on a request config to bypass the
// interceptors selectively.
declare module 'axios' {
  export interface AxiosRequestConfig {
    // Skip the 401-refresh dance — used by /auth/refresh itself to
    // avoid recursive refresh loops.
    _skipRefresh?: boolean
    // Skip the global error toast — used when the caller wants to
    // render its own UI affordance (form field error, etc.).
    _skipErrorToast?: boolean
    // Internal: marks a request that has already attempted refresh
    // once, so a subsequent 401 falls through to the logout path.
    _retried?: boolean
  }
}

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

// Single-flight refresh: while a refresh is in progress, every other
// 401 awaits the same promise rather than firing N parallel refreshes
// (which would race each other and clobber the refresh token).
let refreshInFlight: Promise<string | null> | null = null

async function performRefresh(): Promise<string | null> {
  const refresh = localStorage.getItem('psp_refresh')
  if (!refresh) return null
  try {
    // Bypass our own interceptor + global toast for this call.
    const res = await client.post<{
      access_token: string
      refresh_token: string
    }>('/auth/refresh', { refresh_token: refresh }, {
      _skipRefresh: true,
      _skipErrorToast: true,
    } as AxiosRequestConfig)
    if (res.data?.access_token) {
      localStorage.setItem('psp_access', res.data.access_token)
    }
    if (res.data?.refresh_token) {
      localStorage.setItem('psp_refresh', res.data.refresh_token)
    }
    return res.data?.access_token || null
  } catch {
    return null
  }
}

// Last-toast de-dup: same (status, message) within DEDUP_MS only fires
// the toast once. Lets Promise.allSettled report aggregate failures
// without flooding the snackbar.
const DEDUP_MS = 1500
let lastToast: { key: string; at: number } = { key: '', at: 0 }
function maybePushSnack(key: string, msg: string, level: 'error' | 'warning') {
  const now = Date.now()
  if (lastToast.key === key && now - lastToast.at < DEDUP_MS) return
  lastToast = { key, at: now }
  pushSnack(msg, level)
}

function responseErrorMessage(err: AxiosError<{ error?: string }>): string {
  return err.response?.data?.error || err.message || 'request failed'
}

// categoriseError returns a user-facing string + a stable key for the
// de-dup map. Network / timeout / 5xx / 4xx are surfaced with distinct
// wording so users don't see a raw "Network Error" / "Request failed
// with status code 500".
function categoriseError(err: AxiosError<{ error?: string }>): { key: string; msg: string } {
  const t = i18n.t
  if (err.code === 'ECONNABORTED' || /timeout/i.test(err.message || '')) {
    return { key: 'timeout', msg: t('common:errors.timeout', { defaultValue: '请求超时，请稍后重试' }) }
  }
  if (!err.response) {
    return { key: 'network', msg: t('common:errors.network', { defaultValue: '网络异常，请检查连接' }) }
  }
  const status = err.response.status
  // Prefer the server-provided error text when it's present; that
  // string is already localised by the backend's error mapping.
  const serverMsg = err.response.data?.error
  if (status >= 500) {
    return {
      key: `5xx`,
      msg: serverMsg || t('common:errors.server', { defaultValue: '服务器异常，请稍后重试' }),
    }
  }
  if (status === 429) {
    return { key: '429', msg: serverMsg || t('common:errors.rate_limited', { defaultValue: '请求过于频繁，请稍后再试' }) }
  }
  return { key: `4xx:${status}:${serverMsg || ''}`, msg: serverMsg || responseErrorMessage(err) }
}

function logoutAndRedirect(err: AxiosError<{ error?: string }>) {
  localStorage.removeItem('psp_access')
  localStorage.removeItem('psp_refresh')
  localStorage.removeItem('psp_user')
  const onAuthPublicPage =
    location.pathname === panelURL('/login')
    || location.pathname.startsWith(panelURL('/login/'))
    || location.pathname === panelURL('/logged-out')
  if (onAuthPublicPage) {
    maybePushSnack('401:public', responseErrorMessage(err), 'error')
  } else {
    location.href = panelURL('/login')
  }
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
  async (err: AxiosError<{ error?: string }>) => {
    // --- request was cancelled (AbortController). Do NOT surface a
    // toast — cancellation is an intentional client-side signal (route
    // change, dep change in usePaged, component unmount), not an
    // error condition. Pre-fix categoriseError treated it as "no
    // response → network error", so every usePaged dep-change /
    // navigation away mid-fetch fired a spurious "Network error"
    // toast even though the next request landed fine. This is the
    // root cause of the user-reported "click Users → Network error
    // toast even though the list loaded" symptom — when the URL→state
    // sync briefly re-runs the fetch effect (or React StrictMode
    // double-mounts in dev), the first fetch is cancelled and its
    // CanceledError propagates here.
    if (axios.isCancel(err) || err.code === 'ERR_CANCELED') {
      return Promise.reject(err)
    }
    const cfg = err.config as InternalAxiosRequestConfig | undefined
    // --- 401 with a not-yet-retried request → attempt silent refresh.
    if (err.response?.status === 401 && cfg && !cfg._skipRefresh && !cfg._retried) {
      if (!refreshInFlight) {
        // Clear the slot synchronously once the refresh settles. Concurrent
        // 401s that captured this promise still await the same result; a 401
        // arriving afterwards correctly starts a fresh refresh. (The previous
        // setTimeout(0) clear was racy — a late 401 could reuse an
        // already-failed promise.)
        refreshInFlight = performRefresh().finally(() => { refreshInFlight = null })
      }
      const newAccess = await refreshInFlight
      if (newAccess) {
        cfg._retried = true
        if (cfg.headers) {
          cfg.headers.Authorization = `Bearer ${newAccess}`
        }
        return client(cfg)
      }
      // refresh failed → fall through to the original logout path.
      logoutAndRedirect(err)
      return Promise.reject(err)
    }
    // --- 401 either on the refresh request itself, or after a retry.
    // A request that opted out of the 401 dance (_skipRefresh) handles its own
    // 401 — e.g. a WRONG 2FA code on a self-service enroll/disable endpoint, or
    // the login-time 2FA verify. Those must NOT wipe the session and redirect to
    // /login (a wrong authenticator code is bad input, not an expired session);
    // let the 401 fall through to the caller's catch (and the general path below,
    // which stays quiet under _skipErrorToast). Genuine session-expiry 401s don't
    // set _skipRefresh, so they still refresh-then-logout as before.
    if (err.response?.status === 401 && !cfg?._skipRefresh) {
      logoutAndRedirect(err)
      return Promise.reject(err)
    }
    // --- 403 "must enroll 2FA": the account is required to set up a second
    // factor and the backend gate refuses everything but the enrollment
    // endpoints. Bounce to the enrollment screen instead of a wall of 403
    // toasts. (The screen only hits allowlisted endpoints, so no loop.)
    if (err.response?.status === 403
      && (err.response?.data as { code?: string } | undefined)?.code === '2fa_enrollment_required') {
      if (location.pathname !== panelURL('/enroll-2fa')) {
        location.href = panelURL('/enroll-2fa')
      }
      return Promise.reject(err)
    }
    // --- everything else: categorised + de-duped toast.
    if (!cfg?._skipErrorToast) {
      const status = err.response?.status ?? 0
      const level: 'error' | 'warning' = status === 429 ? 'warning' : 'error'
      const { key, msg } = categoriseError(err)
      maybePushSnack(key, msg, level)
    }
    return Promise.reject(err)
  },
)
