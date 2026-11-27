// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import * as legalApi from '@/api/legal'
import * as meApi from '@/api/me'
import { renderAccess, setTestLang, snack } from '@/test/accessControlHarness'
import { useSiteStore } from '@/stores/site'
import ConsentDialog from './ConsentDialog'

vi.mock('@/api/legal', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/legal')>()
  return { ...real, acceptLegal: vi.fn() }
})
vi.mock('@/api/me', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/me')>()
  return { ...real, getMyProfile: vi.fn() }
})

let pending = true
const outdated = (version: number) => Object.assign(new Error('409'), { response: { status: 409, data: { code: 'legal_consent_outdated', consent_version: version } } })

function mount(role: 'user' | 'admin' | 'operator' = 'user') { return renderAccess(<ConsentDialog />, { route: '/user/me', role }) }

beforeEach(() => {
  pending = true
  sessionStorage.clear()
  useSiteStore.setState({ legalEnabled: true, legalConsentVersion: 3 })
  vi.mocked(meApi.getMyProfile).mockImplementation(async () => ({ legal_pending: pending }) as meApi.MeProfile)
  vi.mocked(legalApi.acceptLegal).mockReset().mockImplementation(async () => { pending = false })
})
afterEach(() => { vi.unstubAllGlobals() })

describe('the consent dialog (S23)', () => {
  it('asks a user whose consent is pending, with the two documents one click away', async () => {
    mount()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('请阅读服务条款和隐私政策')).toBeTruthy()
    expect(within(dialog).getByText(/你的订阅不受影响/)).toBeTruthy()
    for (const [name, href] of [['《服务条款》', '/legal/terms'], ['《隐私政策》', '/legal/privacy']]) {
      const link = within(dialog).getByRole('link', { name })
      expect(link.getAttribute('href')).toBe(href)
      expect(link.getAttribute('target')).toBe('_blank')
    }
  })

  it('never asks staff, and does not even read the profile for them', async () => {
    mount('admin')
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(meApi.getMyProfile).not.toHaveBeenCalled()
  })

  it('shows nothing while the feature is off or nothing is pending', async () => {
    useSiteStore.setState({ legalEnabled: false })
    mount()
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(meApi.getMyProfile).not.toHaveBeenCalled()
  })

  it('shows nothing when the account has nothing pending', async () => {
    pending = false
    mount()
    await waitFor(() => expect(meApi.getMyProfile).toHaveBeenCalled())
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('Agree records the current version, says so, and closes', async () => {
    mount()
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: '同意' }))
    await waitFor(() => expect(legalApi.acceptLegal).toHaveBeenCalledWith(3))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已记录你的同意', 'success'))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('Later closes it for this browser session only', async () => {
    const first = mount()
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '稍后' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(legalApi.acceptLegal).not.toHaveBeenCalled()
    first.unmount()
    mount()
    await waitFor(() => expect(meApi.getMyProfile).toHaveBeenCalledTimes(2))
    expect(screen.queryByRole('dialog')).toBeNull()
    // A new browser session asks again.
    sessionStorage.clear()
    mount()
    expect(await screen.findByRole('dialog')).toBeTruthy()
  })

  it('Esc is Later; a click on the backdrop does nothing', async () => {
    mount()
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(document.querySelector('[data-slot="dialog-overlay"]')!)
    expect(screen.getByRole('dialog')).toBeTruthy()
    fireEvent.keyDown(dialog, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('still asks when session storage cannot be read or written', async () => {
    vi.stubGlobal('sessionStorage', { getItem() { throw new Error('denied') }, setItem() { throw new Error('denied') }, removeItem() {}, clear() {} })
    mount()
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: '稍后' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('a failed submit says so in the dialog and keeps it open', async () => {
    vi.mocked(legalApi.acceptLegal).mockRejectedValue(new Error('network'))
    mount()
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: '同意' }))
    expect(await within(dialog).findByText('提交失败，请重试')).toBeTruthy()
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it('a version that moved says to read again and agrees to the new one next', async () => {
    vi.mocked(legalApi.acceptLegal).mockRejectedValueOnce(outdated(4))
    mount()
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: '同意' }))
    expect(await within(dialog).findByText('条款刚刚又更新了，请重新阅读')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: '同意' }))
    await waitFor(() => expect(legalApi.acceptLegal).toHaveBeenLastCalledWith(4))
  })

  it('offers Later first and Agree second, so stacked on a phone Agree is the lower, last button', async () => {
    mount()
    const dialog = await screen.findByRole('dialog')
    const names = within(dialog).getAllByRole('button').map(b => b.textContent)
    expect(names).toEqual(['稍后', '同意'])
  })

  it('in English', async () => {
    setTestLang('en-US')
    mount()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Please review the Terms of Service and Privacy Policy')).toBeTruthy()
    expect(within(dialog).getByRole('button', { name: 'Agree' })).toBeTruthy()
  })
})
