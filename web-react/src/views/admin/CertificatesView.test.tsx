// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mountPage, toast } from './logs/testHarness'
import CertificatesView from './CertificatesView'

const mount = (entry = '/admin/certs') => mountPage(<CertificatesView />, entry, '/admin/certs')
const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }

const day = (n: number) => new Date(Date.now() + n * 86_400_000).toISOString()
const cert = (over: Record<string, unknown>) => ({ id: 1, name: 'web', domains: ['a.example.test'], acme_account_id: 1, dns_credential_id: 1, auto_renew: true, status: 'active', not_before: day(-30), not_after: day(60), fingerprint: 'ab:cd', last_error: '', created_at: day(-30), ...over })
const account = { id: 1, name: 'ops', email: 'ops@example.test', directory: 'https://acme-v02.api.letsencrypt.org/directory', key_type: 'EC256', eab_key_id: '', has_eab_hmac: false, registered: true, created_at: '' }
const eabAccount = { ...account, id: 2, name: 'zero', email: 'z@example.test', directory: 'https://acme.zerossl.com/v2/DV90', eab_key_id: 'kid-1', has_eab_hmac: true }
const credential = { id: 1, name: 'cf', provider: 'cloudflare', keys: ['CF_DNS_API_TOKEN'] }
const providers = [{ name: 'cloudflare', label: 'Cloudflare', custom: false, fields: [{ key: 'CF_DNS_API_TOKEN', label: 'API token', secret: true }, { key: 'CF_ZONE', label: 'Zone', secret: false, optional: true }] }]
const node = { id: 7, display_name: 'Tokyo 1', panel_name: 'tk', cert_source: 'psp_managed', cert_id: 1 }

function reads(over: Record<string, unknown> = {}) {
  installReads({
    '/admin/certs': { certs: [cert({})] },
    '/admin/dns-credentials': { credentials: [credential] },
    '/admin/acme-accounts': { accounts: [account, eabAccount] },
    '/admin/nodes': list([]),
    '/admin/settings/ui': { cert_renew_before_days: 30, cert_renew_check_interval_hours: 12 },
    '/admin/dns-providers': { providers },
    '/admin/acme-key-types': { key_types: ['EC256', 'RSA2048'] },
    ...over,
  })
}

const rowOf = async (text: string) => (await screen.findByText(text)).closest('tr') as HTMLElement
function openRowMenu(row: HTMLElement, name: RegExp) {
  const trigger = within(row).getByRole('button', { name })
  fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(trigger, { button: 0 })
  fireEvent.click(trigger)
}
// The button is off until the account and credential lists have arrived (there is nothing to issue under before).
async function openNew() {
  const button = await screen.findByRole('button', { name: 'New certificate' }) as HTMLButtonElement
  await waitFor(() => expect(button.disabled).toBe(false))
  fireEvent.click(button)
}
const confirmDialog = () => screen.findByRole('alertdialog')

describe('the certificate list', () => {
  it('sorts problems first and says why, with the CA\'s own words for a failure', async () => {
    reads({
      '/admin/certs': { certs: [
        cert({ id: 1, name: 'fine' }),
        cert({ id: 2, name: 'soon', not_after: day(5.5) }),
        cert({ id: 3, name: 'broken', status: 'failed', last_error: 'acme: urn:ietf:params:acme:error:rateLimited' }),
      ] },
    })
    mount()
    await rowOf('fine')
    const names = within(screen.getByRole('table')).getAllByRole('row').slice(1).map(r => within(r).getByText(/^(fine|soon|broken)$/).textContent)
    expect(names).toEqual(['broken', 'soon', 'fine'])
    expect(within(await rowOf('broken')).getByText('acme: urn:ietf:params:acme:error:rateLimited')).toBeTruthy()
    expect(within(await rowOf('soon')).getByText(/Expires in 5 days; renews automatically/)).toBeTruthy()
  })

  it('links each certificate to the nodes that deploy it', async () => {
    reads({ '/admin/nodes': list([node]) })
    mount()
    const link = await within(await rowOf('web')).findByRole('link', { name: 'Tokyo 1' })
    expect(link.getAttribute('href')).toBe('/admin/nodes/7')
  })

  it('tells an operator the page needs an administrator instead of showing raw errors', async () => {
    const refusal = Object.assign(new Error('forbidden'), { response: { status: 403 } })
    reads({ '/admin/certs': refusal, '/admin/dns-credentials': refusal, '/admin/acme-accounts': refusal })
    mount()
    expect(await screen.findByText('Administrator access required')).toBeTruthy()
  })

  it('reports a failed read with a retry instead of an empty table', async () => {
    reads({ '/admin/certs': new Error('offline'), '/admin/dns-credentials': new Error('offline'), '/admin/acme-accounts': new Error('offline') })
    mount()
    expect(await screen.findByText('Certificate data is temporarily unavailable')).toBeTruthy()
  })
})

describe('renewing', () => {
  it('asks first, naming the certificate and the CA, then queues one renewal and ignores a second click', async () => {
    reads()
    mount()
    const row = await rowOf('web')
    const gate = deferred<{ data: unknown }>()
    api.post.mockImplementation(async () => gate.promise)
    const button = within(row).getByRole('button', { name: 'Renew now' }) as HTMLButtonElement
    fireEvent.click(button)
    const dialog = await confirmDialog()
    expect(within(dialog).getByText(/Request a new certificate for "web" from Let's Encrypt/)).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Renew now' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    expect(api.post).toHaveBeenCalledWith('/admin/certs/1/renew')
    fireEvent.click(button)
    expect(api.post).toHaveBeenCalledTimes(1)
    gate.resolve({ data: {} })
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Renewal queued', 'success'))
  })

  it('does not renew a certificate that is already being issued', async () => {
    reads({ '/admin/certs': { certs: [cert({ status: 'pending', not_after: null, fingerprint: '' })] } })
    mount()
    const button = within(await rowOf('web')).getByRole('button', { name: 'Renew now' })
    expect(button.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(button)
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
  })
})

describe('issuing and editing', () => {
  it('sends the same body as before when issuing', async () => {
    reads()
    mount()
    await openNew()
    const sheet = await screen.findByRole('dialog')
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: ' edge ' } })
    fireEvent.change(within(sheet).getByLabelText(/^Domains/), { target: { value: '*.edge.test, edge.test\nother.test' } })
    api.post.mockResolvedValue({ data: { cert: cert({ id: 9 }) } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    expect(api.post).toHaveBeenCalledWith('/admin/certs', { name: 'edge', domains: ['*.edge.test', 'edge.test', 'other.test'], acme_account_id: 1, dns_credential_id: 1, auto_renew: true })
  })

  it('names what is missing next to the field and sends nothing', async () => {
    reads()
    mount()
    await openNew()
    const sheet = await screen.findByRole('dialog')
    fireEvent.click(within(sheet).getByRole('button', { name: 'Create' }))
    expect(await within(sheet).findByText('Enter a name')).toBeTruthy()
    expect(within(sheet).getByText('Enter at least one domain')).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('confirms before a change of domains re-issues the certificate, and says what it does', async () => {
    reads()
    mount()
    openRowMenu(await rowOf('web'), /More actions for web/)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    const sheet = await screen.findByRole('dialog')
    fireEvent.change(within(sheet).getByLabelText(/^Domains/), { target: { value: 'a.example.test\nb.example.test' } })
    expect(within(sheet).getByText('The domains changed: saving re-issues this certificate.')).toBeTruthy()
    api.put.mockResolvedValue({ data: { cert: cert({ domains: ['a.example.test', 'b.example.test'] }) } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Save' }))
    const dialog = await confirmDialog()
    expect(within(dialog).getByText(/counts toward its rate limits/)).toBeTruthy()
    expect(api.put).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save and re-issue' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/certs/1', { name: 'web', domains: ['a.example.test', 'b.example.test'], acme_account_id: 1, dns_credential_id: 1, auto_renew: true }))
  })
})

describe('deleting', () => {
  it('offers no delete for a certificate nodes still use, and links to them', async () => {
    reads({ '/admin/nodes': list([node]) })
    mount()
    await within(await rowOf('web')).findByRole('link', { name: 'Tokyo 1' })
    openRowMenu(await rowOf('web'), /More actions for web/)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await confirmDialog()
    expect(within(dialog).getByText(/still in use and cannot be deleted/)).toBeTruthy()
    expect(within(dialog).getByRole('link', { name: 'Tokyo 1' }).getAttribute('href')).toBe('/admin/nodes/7')
    expect(within(dialog).queryByRole('button', { name: 'Delete' })).toBeNull()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('offers no delete while it cannot tell which nodes use the certificate, only a retry', async () => {
    reads({ '/admin/nodes': new Error('offline') })
    mount()
    openRowMenu(await rowOf('web'), /More actions for web/)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await confirmDialog()
    expect(within(dialog).getByText(/Could not check which nodes use "web"/)).toBeTruthy()
    expect(within(dialog).queryByRole('button', { name: 'Delete' })).toBeNull()
    expect(within(dialog).getByRole('button', { name: 'Retry' })).toBeTruthy()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('shows the server\'s own reason, with the way forward, when it refuses', async () => {
    reads()
    mount()
    // Delete is only offered once the nodes have been read.
    await waitFor(() => expect(api.get.mock.calls.some(([u]) => u === '/admin/nodes')).toBe(true))
    await new Promise(r => setTimeout(r, 50))
    openRowMenu(await rowOf('web'), /More actions for web/)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await confirmDialog()
    api.delete.mockRejectedValue({ response: { data: { error: 'certificate still bound to 1 node(s); change their certificate source first' } } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(await within(dialog).findByText(/still bound to 1 node/)).toBeTruthy()
    expect(within(dialog).getByText(/change its certificate source/)).toBeTruthy()
    expect(api.delete).toHaveBeenCalledWith('/admin/certs/1')
  })
})

describe('DNS credentials keep their secrets', () => {
  it('never shows a stored value, and an empty box keeps it (blank is sent as before)', async () => {
    reads()
    mount('/admin/certs?tab=credentials')
    fireEvent.click(await screen.findByText('cf'))
    const sheet = await screen.findByRole('dialog')
    const token = within(sheet).getByLabelText(/API token/) as HTMLInputElement
    expect(token.value).toBe('')
    expect(token.type).toBe('password')
    expect(token.placeholder).toBe('Already set; leave empty to keep it')
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'cf2' } })
    api.put.mockResolvedValue({ data: { credential: { ...credential, name: 'cf2' } } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/dns-credentials/1', { name: 'cf2', provider: 'cloudflare', credentials: { CF_DNS_API_TOKEN: '', CF_ZONE: '' } }))
  })

  it('requires every required value when creating, and keeps the draft out of the address', async () => {
    reads()
    const m = mount('/admin/certs?tab=credentials')
    fireEvent.click((await screen.findAllByRole('button', { name: 'New credential' }))[0])
    const sheet = await screen.findByRole('dialog')
    fireEvent.change(within(sheet).getByLabelText(/^Name/), { target: { value: 'new' } })
    fireEvent.change(within(sheet).getByLabelText(/^DNS provider/), { target: { value: 'cloudflare' } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Save' }))
    expect(await within(sheet).findByText('Enter a value')).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
    fireEvent.change(within(sheet).getByLabelText(/API token/), { target: { value: 's3cret' } })
    api.post.mockResolvedValue({ data: { credential: { ...credential, id: 2 } } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/dns-credentials', { name: 'new', provider: 'cloudflare', credentials: { CF_DNS_API_TOKEN: 's3cret' } }))
    expect(m.router.state.location.search).not.toContain('s3cret')
  })
})

describe('ACME accounts', () => {
  it('warns that changing the identity clears the registration, and keeps the HMAC secret unless a new one is typed', async () => {
    reads()
    mount('/admin/certs?tab=accounts')
    fireEvent.click(await screen.findByText('zero'))
    const sheet = await screen.findByRole('dialog')
    const hmac = within(sheet).getByLabelText(/EAB HMAC Key/) as HTMLInputElement
    expect(hmac.value).toBe('')
    expect(within(sheet).queryByText(/clears this account's registration/)).toBeNull()
    fireEvent.change(within(sheet).getByLabelText(/^Email/), { target: { value: 'new@example.test' } })
    expect(within(sheet).getByText(/clears this account's registration/)).toBeTruthy()
    api.put.mockResolvedValue({ data: { account: { ...eabAccount, email: 'new@example.test' } } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/acme-accounts/2', { name: 'zero', email: 'new@example.test', directory: eabAccount.directory, eab_key_id: 'kid-1', eab_hmac: '', key_type: 'EC256' }))
  })

  it('offers no delete while certificates are issued under the account', async () => {
    reads()
    mount('/admin/certs?tab=accounts')
    openRowMenu(await rowOf('ops'), /More actions for ops/)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    const dialog = await confirmDialog()
    expect(within(dialog).getByText(/still in use/)).toBeTruthy()
    expect(within(dialog).queryByRole('button', { name: 'Delete' })).toBeNull()
  })
})

describe('renewal settings', () => {
  it('writes the whole settings record back with only the two numbers changed', async () => {
    reads({ '/admin/settings/ui': { cert_renew_before_days: 30, cert_renew_check_interval_hours: 12, site_name: 'x' } })
    mount()
    const days = await screen.findByLabelText(/Renew N days before expiry/)
    fireEvent.change(days, { target: { value: '21' } })
    fireEvent.blur(days)
    api.put.mockImplementation(async (_u: string, body: unknown) => ({ data: body }))
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', { cert_renew_before_days: 21, cert_renew_check_interval_hours: 12, site_name: 'x' }))
  })
})
