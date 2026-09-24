// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import CertificatesView from './CertificatesView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
const confirmMock = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'zh-CN' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount() {
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <CertificatesView />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

// A deferred promise the test resolves on its own schedule, standing in for a
// slow (3-10s) upstream response.
function deferred<T = void>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

const cert = { id: 1, name: 'cert-a', domains: ['example.test'], acme_account_id: 1, dns_credential_id: 1, auto_renew: true, status: 'active' }
const credential = { id: 1, name: 'cred-a', provider: 'custom', keys: ['TOKEN'] }
const account = { id: 1, name: 'acct-a', email: 'admin@example.test', directory: 'https://acme.test/directory', key_type: 'EC256', eab_key_id: '', has_eab_hmac: true }

// Every read CertificatesView issues on mount, resolved immediately unless a
// test overrides one to stay pending.
function baseGet(url: string) {
  switch (url) {
    case '/admin/certs': return Promise.resolve({ data: { certs: [cert] } })
    case '/admin/dns-credentials': return Promise.resolve({ data: { credentials: [credential] } })
    case '/admin/acme-accounts': return Promise.resolve({ data: { accounts: [account] } })
    case '/admin/dns-providers': return Promise.resolve({ data: { providers: [] } })
    case '/admin/acme-key-types': return Promise.resolve({ data: { key_types: ['EC256'] } })
    case '/admin/settings/ui': return Promise.resolve({ data: {} })
    default: return Promise.reject(new Error(`unexpected GET ${url}`))
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  confirmMock.mockResolvedValue(true)
  api.get.mockImplementation(baseGet)
})
afterEach(cleanup)

describe('certificate row actions show busy feedback on a slow network', () => {
  it('keeps the renew icon busy until the request settles, and ignores a second click', async () => {
    const pending = deferred()
    api.post.mockReturnValue(pending.promise)
    mount()

    const row = (await screen.findByText('cert-a')).closest('tr')!
    const renewButton = within(row).getByTestId('AutorenewIcon').closest('button')!
    await waitFor(() => expect((renewButton as HTMLButtonElement).disabled).toBe(false))

    fireEvent.click(renewButton)
    expect((renewButton as HTMLButtonElement).disabled).toBe(true)
    expect(renewButton.getAttribute('aria-busy')).toBe('true')
    expect(within(renewButton).getByRole('progressbar')).toBeTruthy()

    fireEvent.click(renewButton)
    expect(api.post).toHaveBeenCalledTimes(1)

    pending.resolve()
    await waitFor(() => expect((renewButton as HTMLButtonElement).disabled).toBe(false))
    expect(renewButton.getAttribute('aria-busy')).toBeNull()
    expect(within(renewButton).getByTestId('AutorenewIcon')).toBeTruthy()
  })

  it('keeps the delete-certificate icon busy until the request settles, and ignores a second click', async () => {
    const pending = deferred()
    api.delete.mockReturnValue(pending.promise)
    mount()

    const row = (await screen.findByText('cert-a')).closest('tr')!
    const deleteButton = within(row).getByTestId('DeleteOutlinedIcon').closest('button')!
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))

    fireEvent.click(deleteButton)
    // Busy immediately — before the confirm dialog's own promise, let alone
    // the delete request, has had a chance to settle.
    expect((deleteButton as HTMLButtonElement).disabled).toBe(true)
    expect(deleteButton.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
    fireEvent.click(deleteButton)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.delete).toHaveBeenCalledTimes(1)
    expect(within(deleteButton).getByRole('progressbar')).toBeTruthy()

    pending.resolve()
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))
    expect(deleteButton.getAttribute('aria-busy')).toBeNull()
  })

  it('keeps the delete-DNS-credential icon busy until the request settles, and ignores a second click', async () => {
    const pending = deferred()
    api.delete.mockReturnValue(pending.promise)
    mount()

    fireEvent.click((await screen.findAllByRole('tab'))[1])
    const row = (await screen.findByText('cred-a')).closest('tr')!
    const deleteButton = within(row).getByTestId('DeleteOutlinedIcon').closest('button')!
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))

    fireEvent.click(deleteButton)
    expect((deleteButton as HTMLButtonElement).disabled).toBe(true)
    expect(deleteButton.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
    fireEvent.click(deleteButton)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.delete).toHaveBeenCalledTimes(1)
    expect(within(deleteButton).getByRole('progressbar')).toBeTruthy()

    pending.resolve()
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))
    expect(deleteButton.getAttribute('aria-busy')).toBeNull()
  })

  it('keeps the delete-ACME-account icon busy until the request settles, and ignores a second click', async () => {
    const pending = deferred()
    api.delete.mockReturnValue(pending.promise)
    mount()

    fireEvent.click((await screen.findAllByRole('tab'))[2])
    const row = (await screen.findByText('acct-a')).closest('tr')!
    const deleteButton = within(row).getByTestId('DeleteOutlinedIcon').closest('button')!
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))

    fireEvent.click(deleteButton)
    expect((deleteButton as HTMLButtonElement).disabled).toBe(true)
    expect(deleteButton.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
    fireEvent.click(deleteButton)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.delete).toHaveBeenCalledTimes(1)
    expect(within(deleteButton).getByRole('progressbar')).toBeTruthy()

    pending.resolve()
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))
    expect(deleteButton.getAttribute('aria-busy')).toBeNull()
  })
})

describe('DNS credentials / ACME accounts tabs while their read is still in flight', () => {
  it('shows a loading indicator instead of the empty state before the reads resolve', async () => {
    const pendingCreds = deferred<{ data: { credentials: unknown[] } }>()
    const pendingAccounts = deferred<{ data: { accounts: unknown[] } }>()
    api.get.mockImplementation((url: string) => {
      if (url === '/admin/certs') return Promise.resolve({ data: { certs: [] } })
      if (url === '/admin/dns-credentials') return pendingCreds.promise
      if (url === '/admin/acme-accounts') return pendingAccounts.promise
      return baseGet(url)
    })
    mount()

    const tabs = await screen.findAllByRole('tab')

    fireEvent.click(tabs[1]) // DNS credentials
    expect(screen.queryByText('common:empty')).toBeNull()
    expect(screen.getAllByRole('progressbar').length).toBeGreaterThan(0)

    const acctEmptyText = '还没有 ACME 账号。新建一个后才能签发证书。'
    fireEvent.click(tabs[2]) // ACME accounts
    expect(screen.queryByText(acctEmptyText)).toBeNull()
    expect(screen.getAllByRole('progressbar').length).toBeGreaterThan(0)

    pendingCreds.resolve({ data: { credentials: [] } })
    pendingAccounts.resolve({ data: { accounts: [] } })

    await waitFor(() => expect(screen.getByText(acctEmptyText)).toBeTruthy())
    fireEvent.click(tabs[1])
    await waitFor(() => expect(screen.getByText('common:empty')).toBeTruthy())
  })
})
