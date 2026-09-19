// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'


const mocks = vi.hoisted(() => ({
  loginSSO: vi.fn(),
  hasToken: false,
  navigate: vi.fn(),
}))

vi.mock('@/stores/auth', () => {
  // The component reads the session through the hook (and, after the fix, through
  // getState), so both must carry the same state — including the exchange itself.
  const state = () => ({ hasToken: mocks.hasToken, loginSSO: mocks.loginSSO })
  const hook = () => state()
  return { useAuthStore: Object.assign(hook, { getState: state }) }
})
vi.mock('react-router', () => ({
  useNavigate: () => mocks.navigate,
  useSearchParams: () => [new URLSearchParams('next=%2Fuser%2Fme')],
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k: string) => k }),
}))
vi.mock('@mui/material', () => ({
  Box: ({ children }: { children?: unknown }) => <div>{children as never}</div>,
  CircularProgress: () => <div />,
  Typography: ({ children }: { children?: unknown }) => <div>{children as never}</div>,
  useTheme: () => ({ palette: { md: {} } }),
}))

import SsoCallbackView from './SsoCallbackView'

const unauthorized = { response: { status: 401, data: { error: 'No sso session' } } }

beforeEach(() => {
  mocks.loginSSO.mockReset()
  mocks.navigate.mockReset()
  mocks.hasToken = false
})

describe('the SSO callback page', () => {
  it('navigates on when the exchange succeeds', async () => {
    mocks.loginSSO.mockResolvedValue(undefined)
    render(<SsoCallbackView />)
    await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith('/user/me', { replace: true }))
  })

  // The exchange is single-use: it spends the ACS cookies, so a second ask is
  // answered 401 even though the session is real. This page can be mounted twice
  // — a remount, a reload, two tabs — and the loser would otherwise strand a
  // signed-in person on an error screen reading the server's own error text.
  it('navigates on when another instance already exchanged the session', async () => {
    mocks.loginSSO.mockRejectedValue(unauthorized)
    mocks.hasToken = true
    render(<SsoCallbackView />)
    await waitFor(() => expect(mocks.navigate).toHaveBeenCalledWith('/user/me', { replace: true }))
  })

  // The tolerance above must not swallow a real failure: with no session there is
  // nothing to carry on with, and the reason belongs on screen.
  it('shows the reason when the exchange fails and there is no session', async () => {
    mocks.loginSSO.mockRejectedValue(unauthorized)
    mocks.hasToken = false
    render(<SsoCallbackView />)
    await waitFor(() => expect(screen.getByText('No sso session')).toBeTruthy())
    expect(mocks.navigate).not.toHaveBeenCalled()
  })
})
