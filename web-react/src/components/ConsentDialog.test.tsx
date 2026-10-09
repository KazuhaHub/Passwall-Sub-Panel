// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { flatten, type Nested } from '@/i18n/options'
import user from '@/locales/zh-CN/user.json'
import auth from '@/locales/zh-CN/auth.json'
import ConsentDialog from './ConsentDialog'
import LegalFooter from './LegalFooter'

const mocks = vi.hoisted(() => ({ accept: vi.fn(), snack: vi.fn(), refresh: vi.fn(), user: {} as Record<string, string>, auth: {} as Record<string, string> }))
vi.mock('@/api/legal', () => ({ acceptLegalConsent: mocks.accept }))
vi.mock('./SnackbarHost', () => ({ pushSnack: mocks.snack }))
vi.mock('react-i18next', () => ({ useTranslation: (namespace: string) => ({
  t: (key: string) => (namespace === 'user' ? mocks.user : mocks.auth)[key] ?? key,
  i18n: { language: 'zh-CN', resolvedLanguage: 'zh-CN' },
}) }))
mocks.user = flatten(user as Nested)
mocks.auth = flatten(auth as Nested)

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })
function view(version = 2, sessionKey = 'session-one', pending = true) {
  return <MemoryRouter><ThemeProvider theme={theme}><ConsentDialog pending={pending} version={version} sessionKey={sessionKey} onRefresh={mocks.refresh} /></ThemeProvider></MemoryRouter>
}
beforeEach(() => { vi.clearAllMocks(); sessionStorage.clear(); mocks.accept.mockResolvedValue(undefined); mocks.refresh.mockResolvedValue(undefined) })
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('ConsentDialog', () => {
  it('records the displayed version, refreshes profile and closes on success', async () => {
    render(view())
    fireEvent.click(screen.getByRole('button', { name: '同意' }))
    await waitFor(() => expect(mocks.accept).toHaveBeenCalledWith(2))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(mocks.refresh).toHaveBeenCalledTimes(1)
    expect(mocks.snack).toHaveBeenCalledWith('已记录你的同意', 'success')
  })

  it('refreshes after 409 and keeps the updated reading links and warning', async () => {
    mocks.accept.mockRejectedValueOnce({ response: { status: 409 } })
    const { rerender } = render(view(2))
    fireEvent.click(screen.getByRole('button', { name: '同意' }))
    expect(await screen.findByText('条款刚刚又更新了，请重新阅读')).toBeTruthy()
    await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1))
    rerender(view(3))
    expect(screen.getByRole('link', { name: '服务条款' }).getAttribute('href')).toContain('&v=3')
    fireEvent.click(screen.getByRole('button', { name: '同意' }))
    await waitFor(() => expect(mocks.accept).toHaveBeenLastCalledWith(3))
  })

  it('shows an acceptance failure and supports retry', async () => {
    mocks.accept.mockRejectedValueOnce(new Error('offline'))
    render(view())
    fireEvent.click(screen.getByRole('button', { name: '同意' }))
    expect(await screen.findByText('提交失败，请重试')).toBeTruthy()
    expect(mocks.refresh).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '同意' }))
    await waitFor(() => expect(mocks.accept).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('does not turn a committed consent into failure if profile refresh fails', async () => {
    mocks.refresh.mockRejectedValue(new Error('profile offline'))
    render(view())
    fireEvent.click(screen.getByRole('button', { name: '同意' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(mocks.snack).toHaveBeenCalledWith('已记录你的同意', 'success')
    expect(screen.queryByText('提交失败，请重试')).toBeNull()
  })

  it('treats Escape as later for this login and asks again in a new login', async () => {
    const first = render(view(2, 'account-1-login-1'))
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape', code: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(sessionStorage.getItem('account-1-login-1')).toBe('later')
    first.unmount()
    const second = render(view(2, 'account-1-login-1'))
    expect(screen.queryByRole('dialog')).toBeNull()
    second.unmount()
    render(view(2, 'account-1-login-2'))
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it('ignores backdrop clicks', () => {
    const { baseElement } = render(view())
    const backdrop = baseElement.querySelector('.MuiBackdrop-root')
    expect(backdrop).not.toBeNull()
    fireEvent.mouseDown(backdrop!)
    fireEvent.click(backdrop!)
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect(sessionStorage.getItem('session-one')).toBeNull()
  })

  it('remains usable when session storage reads and writes throw', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('unavailable') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('unavailable') })
    render(view())
    expect(screen.getByRole('dialog')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '稍后' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('does not open for a nonpending or unpublished profile', () => {
    const { rerender } = render(view(2, 'one', false))
    expect(screen.queryByRole('dialog')).toBeNull()
    rerender(view(0, 'one', true))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})

describe('LegalFooter', () => {
  it('renders enabled links with an empty footer and hides them when disabled', () => {
    const wrap = (enabled: boolean) => <MemoryRouter><ThemeProvider theme={theme}><LegalFooter text="" enabled={enabled} /></ThemeProvider></MemoryRouter>
    const { rerender } = render(wrap(true))
    expect(screen.getByRole('link', { name: '服务条款' })).toBeTruthy()
    expect(screen.getByRole('link', { name: '隐私政策' })).toBeTruthy()
    rerender(wrap(false))
    expect(screen.queryByRole('contentinfo')).toBeNull()
  })
})
