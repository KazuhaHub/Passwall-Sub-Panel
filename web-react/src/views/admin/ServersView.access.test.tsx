// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mount, server } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import { destinationNode, destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'
import ServersView from './ServersView'

const P = 'admin:access_control.'
const native = { ...server, name: 'Native Tokyo', panel_type: 'psp' }
const legacy = { ...server, id: 2, name: 'Legacy panel' }
const row = (name: string) => screen.getByText(name).closest('tr')!
const reads = (url: string) => api.get.mock.calls.filter(([path]) => path === url)
function setup(overrides: Record<string, unknown> = {}) {
  installReads({ '/admin/servers': list([native, legacy]), '/admin/dest/status': destinationStatus(),
    '/admin/dest/policies': destinationPolicies(), ...overrides })
}

it('shows the access line only on a native row, opens its details and links to fleet coverage', async () => {
  setup()
  mount(<ServersView />)
  const open = await within(await screen.findByText('Native Tokyo').then(() => row('Native Tokyo'))).findByRole('button', { name: `${P}server.line` })
  expect(within(row('Legacy panel')).queryByRole('button', { name: `${P}server.line` })).toBeNull()
  fireEvent.click(open)
  const dialog = await screen.findByRole('dialog', { name: `${P}server.title` })
  expect(within(dialog).getByText('Tokyo')).toBeTruthy()
  expect(within(dialog).getByRole('link', { name: `${P}server.open_access` }).getAttribute('href')).toBe('/admin/access-control?sheet=nodes')
  expect(within(dialog).getByText(`${P}coverage.executing_rules 1`)).toBeTruthy()
  expect(reads('/admin/dest/status')).toHaveLength(1)
  expect(reads('/admin/dest/policies')).toHaveLength(1)
  expect(within(dialog).queryByText(/collect|hits_24h|allowlist_groups/)).toBeNull()
})

it('opens access details from the native action menu without posting a node action', async () => {
  setup()
  mount(<ServersView />)
  await screen.findByText('Native Tokyo')
  fireEvent.click(within(row('Native Tokyo')).getByRole('button', { name: 'admin:servers.action.more' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: `${P}server.menu` }))
  await screen.findByRole('dialog', { name: `${P}server.title` })
  expect(api.post.mock.calls.filter(([url]) => url.startsWith('/admin/dest/'))).toHaveLength(0)
})

it('does not add row noise when the entire fleet has neither saved nor published policies', async () => {
  setup({ '/admin/dest/policies': destinationPolicies({ block: [], published_has_access_control: false }),
    '/admin/dest/status': destinationStatus({ nodes: [destinationNode({ state: 'offline' })] }) })
  mount(<ServersView />)
  await waitFor(() => expect(reads('/admin/dest/status')).toHaveLength(1))
  await waitFor(() => expect(reads('/admin/dest/policies')).toHaveLength(1))
  expect(screen.queryByRole('button', { name: `${P}server.line` })).toBeNull()
  expect(screen.getByText('Native Tokyo')).toBeTruthy()
})

it.each([
  { block: [], published_has_access_control: true },
  { published_has_access_control: false },
  { block: [], published_has_access_control: false, allowlist_groups: [{ group_id: 9, name: 'Trial group', stage: 'trial' as const, stage_days: 7 }] },
])('shows an expected or still-published fleet policy without guessing from an offline badge', async definitions => {
  setup({ '/admin/dest/policies': destinationPolicies(definitions) })
  mount(<ServersView />)
  await screen.findByRole('button', { name: `${P}server.line` })
})

it.each(['/admin/dest/status', '/admin/dest/policies'])('isolates failed %s reads from the server list', async failedURL => {
  setup()
  const original = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, ...args: unknown[]) => url === failedURL
    ? Promise.reject({ response: { status: 503 } }) : original(url, ...args))
  const { client } = mount(<ServersView />)
  await screen.findByText('Native Tokyo')
  await waitFor(() => expect(client.getQueryCache().getAll().some(q => q.state.status === 'error' && q.queryKey.includes(failedURL.endsWith('status') ? 'status' : 'policies'))).toBe(true))
  expect(screen.queryByRole('button', { name: `${P}server.line` })).toBeNull()
  expect(screen.queryByText('admin:servers.load_failed')).toBeNull()
})

it.each(['/admin/dest/status', '/admin/dest/policies'])('rejects a malformed 200 response from %s without breaking the server table', async url => {
  setup({ [url]: '<!doctype html><html>SPA fallback</html>' })
  const { client } = mount(<ServersView />)
  await screen.findByText('Native Tokyo')
  await waitFor(() => expect(client.getQueryCache().getAll().some(q => q.state.status === 'error' && q.queryKey.includes(url.endsWith('status') ? 'status' : 'policies'))).toBe(true))
  expect(screen.queryByRole('button', { name: `${P}server.line` })).toBeNull()
})

it('drops an open access dialog when the session loses access permission', async () => {
  setup()
  mount(<ServersView />)
  fireEvent.click(await screen.findByRole('button', { name: `${P}server.line` }))
  await screen.findByRole('dialog', { name: `${P}server.title` })
  const count = reads('/admin/dest/status').length
  act(() => useAuthStore.setState({ role: 'operator', authEpoch: 100 }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(reads('/admin/dest/status')).toHaveLength(count)
})

it.each(['legacy', 'operator'])('does not request destination status/definitions for %s-only visibility', async kind => {
  if (kind === 'operator') useAuthStore.setState({ role: 'operator' })
  setup({ '/admin/servers': list(kind === 'legacy' ? [legacy] : [native]) })
  mount(<ServersView />)
  await screen.findByText(kind === 'legacy' ? 'Legacy panel' : 'Native Tokyo')
  expect(reads('/admin/dest/status')).toHaveLength(0)
  expect(reads('/admin/dest/policies')).toHaveLength(0)
  expect(screen.queryByRole('button', { name: `${P}server.line` })).toBeNull()
})

it('hides a row after failed refresh while an open detail retains explicitly stale receipts and retries reads', async () => {
  setup()
  let failed = false
  const original = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, ...args: unknown[]) => url === '/admin/dest/status' && failed
    ? Promise.reject({ response: { status: 503 } }) : original(url, ...args))
  const { client } = mount(<ServersView />)
  fireEvent.click(await screen.findByRole('button', { name: `${P}server.line` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}server.title` })
  failed = true
  await act(async () => { await client.refetchQueries({ queryKey: ['private'] }) })
  await within(dialog).findByText(`${P}coverage.read_stale`)
  expect(within(dialog).getByText('Tokyo')).toBeTruthy()
  expect(screen.queryByRole('button', { name: `${P}server.line` })).toBeNull()
  failed = false
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.retry' }))
  await waitFor(() => expect(within(dialog).queryByText(`${P}coverage.read_stale`)).toBeNull())
  expect(api.post.mock.calls.filter(([url]) => url.startsWith('/admin/dest/'))).toHaveLength(0)
})

it('keeps cold details loading distinct from missing node status and exposes a read retry', async () => {
  setup()
  let finish!: (value: unknown) => void
  const pending = new Promise(resolve => { finish = resolve })
  const original = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, ...args: unknown[]) => url === '/admin/dest/status' ? pending : original(url, ...args))
  mount(<ServersView />)
  await screen.findByText('Native Tokyo')
  fireEvent.click(within(row('Native Tokyo')).getByRole('button', { name: 'admin:servers.action.more' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: `${P}server.menu` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}server.title` })
  expect(within(dialog).getByRole('progressbar', { name: `${P}coverage.loading` })).toBeTruthy()
  expect(within(dialog).queryByText(`${P}coverage.unavailable`)).toBeNull()
  await act(async () => finish({ data: destinationStatus({ nodes: [] }) }))
  await within(dialog).findByText(`${P}coverage.unavailable`)
  expect(within(dialog).getByRole('button', { name: 'common:actions.retry' })).toBeTruthy()
  expect(api.post.mock.calls.filter(([url]) => url.startsWith('/admin/dest/'))).toHaveLength(0)
})

it('does not claim an applied receipt matches unpublished saved changes', async () => {
  setup({ '/admin/dest/status': destinationStatus({ generation: 2, published_generation: 1 }) })
  mount(<ServersView />)
  fireEvent.click(await screen.findByRole('button', { name: `${P}server.line` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}server.title` })
  expect(within(dialog).getByText(`${P}server.unpublished`)).toBeTruthy()
  expect(within(dialog).getByText(`${P}coverage.executing_rules 1`)).toBeTruthy()
})

it('admits a single node retry and refreshes the shared fleet receipt afterward', async () => {
  setup({ '/admin/dest/status': destinationStatus({ nodes: [destinationNode({ state: 'rejected' })] }) })
  const writes = () => api.post.mock.calls.filter(([url]) => url.startsWith('/admin/dest/'))
  const { client } = mount(<ServersView />)
  fireEvent.click(await screen.findByRole('button', { name: `${P}server.line` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}server.title` })
  let finish!: (value: unknown) => void
  const pending = new Promise(resolve => { finish = resolve })
  const original = api.post.getMockImplementation()!
  api.post.mockImplementation((url: string, ...args: unknown[]) => url.startsWith('/admin/dest/') ? pending : original(url, ...args))
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const button = within(dialog).getByRole('button', { name: `${P}coverage.retry` })
  fireEvent.click(button); fireEvent.click(button)
  await waitFor(() => expect(writes()).toHaveLength(1))
  expect(writes()[0][0]).toBe('/admin/dest/agents/node-one/retry')
  expect(button.getAttribute('aria-busy')).toBe('true')
  await act(async () => finish({ data: { retry_requested: true } }))
  await waitFor(() => expect(invalidate).toHaveBeenCalledWith(expect.objectContaining({ queryKey: expect.arrayContaining(['status']) })))
})
