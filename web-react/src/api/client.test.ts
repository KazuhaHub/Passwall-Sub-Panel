import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => {
  const state: {
    request?: (config: Record<string, unknown>) => Record<string, unknown>
    responseOK?: (response: Record<string, unknown>) => Record<string, unknown>
    responseError?: (error: Record<string, unknown>) => Promise<unknown>
  } = {}
  const http = Object.assign(vi.fn(), {
    post: vi.fn(),
    interceptors: {
      request: { use: vi.fn((fn: typeof state.request) => { state.request = fn }) },
      response: {
        use: vi.fn((ok: typeof state.responseOK, fail: typeof state.responseError) => {
          state.responseOK = ok
          state.responseError = fail
        }),
      },
    },
  })
  return {
    state,
    http,
    pushSnack: vi.fn(),
    translate: vi.fn((key: string, options?: { defaultValue?: string }) => options?.defaultValue ?? key),
  }
})

vi.mock('axios', () => ({
  default: {
    create: vi.fn(() => mocks.http),
    isCancel: vi.fn((error: { cancelled?: boolean }) => error.cancelled === true),
  },
}))
vi.mock('@/i18n', () => ({ default: { t: mocks.translate } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: mocks.pushSnack }))
vi.mock('@/panelPath', () => ({
  panelAPIBase: '/panel/api',
  panelURL: (route = '/') => `/panel${route}`,
}))

class MemoryStorage implements Storage {
  private values = new Map<string, string>()
  get length() { return this.values.size }
  clear() { this.values.clear() }
  getItem(key: string) { return this.values.get(key) ?? null }
  key(index: number) { return [...this.values.keys()][index] ?? null }
  removeItem(key: string) { this.values.delete(key) }
  setItem(key: string, value: string) { this.values.set(key, String(value)) }
}

const locationStub = { pathname: '/panel/dashboard', href: '' }

beforeAll(async () => {
  vi.stubGlobal('localStorage', new MemoryStorage())
  vi.stubGlobal('location', locationStub)
  await import('./client')
})

beforeEach(() => {
  localStorage.clear()
  locationStub.pathname = '/panel/dashboard'
  locationStub.href = ''
  mocks.http.mockReset()
  mocks.http.post.mockReset()
  mocks.pushSnack.mockReset()
  mocks.translate.mockClear()
  vi.useRealTimers()
})

describe('shared API client interceptors', () => {
  it('attaches the current access token to requests', () => {
    localStorage.setItem('psp_access', 'access-token')
    const config = { headers: {} as Record<string, string> }

    expect(mocks.state.request?.(config)).toBe(config)
    expect(config.headers.Authorization).toBe('Bearer access-token')
  })

  it('throttles sync-pending response notifications', () => {
    vi.useFakeTimers()
    vi.setSystemTime(4_000)
    const response = { headers: { 'x-sync-pending': '1' } }

    expect(mocks.state.responseOK?.(response)).toBe(response)
    vi.setSystemTime(5_000)
    mocks.state.responseOK?.(response)
    expect(mocks.pushSnack).toHaveBeenCalledTimes(1)
    expect(mocks.pushSnack).toHaveBeenCalledWith('common:errors.sync_pending', 'warning')

    vi.setSystemTime(7_001)
    mocks.state.responseOK?.(response)
    expect(mocks.pushSnack).toHaveBeenCalledTimes(2)
  })

  it('does not turn intentional cancellation into an error toast', async () => {
    const error = { cancelled: true, code: 'ERR_CANCELED', config: {} }
    await expect(mocks.state.responseError?.(error)).rejects.toBe(error)
    expect(mocks.pushSnack).not.toHaveBeenCalled()
  })

  it('single-flights refresh and replays concurrent 401 requests', async () => {
    localStorage.setItem('psp_refresh', 'old-refresh')
    mocks.http.post.mockResolvedValue({
      data: { access_token: 'new-access', refresh_token: 'new-refresh' },
    })
    mocks.http.mockResolvedValue({ data: 'replayed' })
    const first = { response: { status: 401 }, message: 'expired', config: { headers: {} } }
    const second = { response: { status: 401 }, message: 'expired', config: { headers: {} } }

    const [a, b] = await Promise.all([
      mocks.state.responseError?.(first),
      mocks.state.responseError?.(second),
    ])

    expect(a).toEqual({ data: 'replayed' })
    expect(b).toEqual({ data: 'replayed' })
    expect(mocks.http.post).toHaveBeenCalledTimes(1)
    expect(mocks.http).toHaveBeenCalledTimes(2)
    expect(first.config).toMatchObject({ _retried: true, headers: { Authorization: 'Bearer new-access' } })
    expect(second.config).toMatchObject({ _retried: true, headers: { Authorization: 'Bearer new-access' } })
    expect(localStorage.getItem('psp_refresh')).toBe('new-refresh')
  })

  it('clears the session and redirects when refresh fails', async () => {
    localStorage.setItem('psp_access', 'expired')
    localStorage.setItem('psp_refresh', 'bad-refresh')
    localStorage.setItem('psp_user', '{}')
    mocks.http.post.mockRejectedValue(new Error('refresh rejected'))
    const error = { response: { status: 401 }, message: 'expired', config: { headers: {} } }

    await expect(mocks.state.responseError?.(error)).rejects.toBe(error)
    expect(localStorage.getItem('psp_access')).toBeNull()
    expect(localStorage.getItem('psp_refresh')).toBeNull()
    expect(localStorage.getItem('psp_user')).toBeNull()
    expect(locationStub.href).toBe('/panel/login')
  })

  it('leaves caller-managed 401s alone and redirects enrollment-required 403s', async () => {
    localStorage.setItem('psp_access', 'keep-me')
    const managed401 = {
      response: { status: 401 },
      message: 'wrong code',
      config: { _skipRefresh: true, _skipErrorToast: true },
    }
    await expect(mocks.state.responseError?.(managed401)).rejects.toBe(managed401)
    expect(localStorage.getItem('psp_access')).toBe('keep-me')
    expect(locationStub.href).toBe('')

    const enroll403 = {
      response: { status: 403, data: { code: '2fa_enrollment_required' } },
      message: 'enroll',
      config: {},
    }
    await expect(mocks.state.responseError?.(enroll403)).rejects.toBe(enroll403)
    expect(locationStub.href).toBe('/panel/enroll-2fa')
  })

  it('categorises and de-duplicates repeated timeout errors', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(20_000)
    const timeout = { code: 'ECONNABORTED', message: 'timeout', config: {} }

    await expect(mocks.state.responseError?.(timeout)).rejects.toBe(timeout)
    await expect(mocks.state.responseError?.(timeout)).rejects.toBe(timeout)
    expect(mocks.pushSnack).toHaveBeenCalledTimes(1)
    expect(mocks.pushSnack).toHaveBeenCalledWith(expect.any(String), 'error')
  })
})
