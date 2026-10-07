// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { useLocation, useNavigate } from 'react-router'
import { expect, it } from 'vitest'
import { api, installReads, list, mount, node, server, snack } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import NodeIssuesView from './NodeIssuesView'
import ServersView from './ServersView'
import NodesView from './NodesView'

function RouteProbe({ next }: { next: string }) {
  const location = useLocation()
  const navigate = useNavigate()
  return <>
    <output data-testid="route">{location.search}</output>
    <button onClick={() => navigate(next)}>Navigate</button>
    <button onClick={() => navigate(-1)}>Back</button>
    <button onClick={() => navigate(1)}>Forward</button>
  </>
}
const route = () => new URLSearchParams(screen.getByTestId('route').textContent || '')
const reads = (url: string) => api.get.mock.calls.filter(([path]) => path === url)
const agentInput = () => screen.getByRole('textbox', { name: 'admin:node_issues.agent_filter' })
const searchInput = () => screen.getByPlaceholderText('admin:servers.search_placeholder')

it('restores the exact agent identity, commits the filter on submit, and clears only that URL parameter', async () => {
  installReads({ '/admin/node-issues': list([]) })
  mount(<><NodeIssuesView /><RouteProbe next="?agent=next&keep=1" /></>, ['/?agent=agt_%E9%93%B6%E8%A1%8C%2F42&keep=1'])
  await waitFor(() => expect(reads('/admin/node-issues').at(-1)?.[1].params.agent_id).toBe('agt_银行/42'))
  expect((agentInput() as HTMLInputElement).value).toBe('agt_银行/42')
  const count = reads('/admin/node-issues').length
  fireEvent.change(agentInput(), { target: { value: '  agt_next  ' } })
  expect(reads('/admin/node-issues')).toHaveLength(count)
  fireEvent.click(screen.getByRole('button', { name: 'admin:node_issues.search_action' }))
  await waitFor(() => expect(reads('/admin/node-issues').at(-1)?.[1].params.agent_id).toBe('agt_next'))
  expect(route().get('agent')).toBe('agt_next')
  fireEvent.click(screen.getByRole('button', { name: 'admin:node_issues.clear_agent' }))
  await waitFor(() => expect(reads('/admin/node-issues').at(-1)?.[1].params.agent_id).toBeUndefined())
  expect(route().get('agent')).toBeNull()
  expect(route().get('keep')).toBe('1')
  expect(api.post).not.toHaveBeenCalled()
})

it('updates the agent input and request on same-page navigation and Back/Forward', async () => {
  installReads({ '/admin/node-issues': list([]) })
  mount(<><NodeIssuesView /><RouteProbe next="?agent=next&keep=1" /></>, ['/?agent=original&keep=1'])
  await waitFor(() => expect(reads('/admin/node-issues').at(-1)?.[1].params.agent_id).toBe('original'))
  for (const [button, expected] of [['Navigate', 'next'], ['Back', 'original'], ['Forward', 'next']]) {
    fireEvent.click(screen.getByRole('button', { name: button }))
    await waitFor(() => expect(reads('/admin/node-issues').at(-1)?.[1].params.agent_id).toBe(expected))
    expect((agentInput() as HTMLInputElement).value).toBe(expected)
  }
})

it('keeps server URL search visible and applied when the input loses focus', async () => {
  installReads({ '/admin/servers': list([]) })
  mount(<><ServersView /><RouteProbe next="?q=second&keep=1" /></>, ['/?q=Node%20East%20%2F%20%E9%93%B6%E8%A1%8C&keep=1'])
  await waitFor(() => expect(reads('/admin/servers').at(-1)?.[1].params.keyword).toBe('Node East / 银行'))
  expect((searchInput() as HTMLInputElement).value).toBe('Node East / 银行')
  fireEvent.blur(searchInput())
  await waitFor(() => expect(route().get('q')).toBe('Node East / 银行'))
  expect(reads('/admin/servers').at(-1)?.[1].params.keyword).toBe('Node East / 银行')
  expect(api.post).not.toHaveBeenCalled()
})

it('restores server search on Back/Forward and accepts another link without remounting', async () => {
  const defaultGet = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, options?: { params?: { keyword?: string } }) => url === '/admin/servers'
    ? Promise.resolve({ data: list([{ ...server, name: options?.params?.keyword }]) })
    : defaultGet(url, options))
  mount(<><ServersView /><RouteProbe next="?q=second&keep=1" /></>, ['/?q=first&keep=1'])
  await waitFor(() => expect((searchInput() as HTMLInputElement).value).toBe('first'))
  for (const [button, expected] of [['Navigate', 'second'], ['Back', 'first'], ['Forward', 'second']]) {
    fireEvent.click(screen.getByRole('button', { name: button }))
    await waitFor(() => expect((searchInput() as HTMLInputElement).value).toBe(expected))
    // Back may reuse the cached query: assert the displayed result, not a redundant GET.
    await screen.findByText(expected)
  }
})

const inbound = { id: 777, protocol: 'vless', remark: 'upstream', enable: true, port: 443, listen: '',
  settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"none"}', sniffing: '{}', allocate: '' }
const managedNode = { ...node, id: 42, inbound_id: 777 }

it('opens the managed node ID once after async list readiness and consumes only the inbound parameter', async () => {
  let finish!: (value: unknown) => void
  installReads({ '/admin/nodes': list([managedNode]), '/admin/nodes/42': { node: managedNode, inbound } })
  const defaultGet = api.get.getMockImplementation()!
  const pending = new Promise(resolve => { finish = resolve })
  api.get.mockImplementation((url: string, ...args: unknown[]) => url === '/admin/nodes' ? pending : defaultGet(url, ...args))
  const { client } = mount(<><NodesView /><RouteProbe next="?inbound=42&keep=1" /></>, ['/?inbound=42&keep=1'])
  await waitFor(() => expect(reads('/admin/nodes').length).toBeGreaterThan(0))
  expect(reads('/admin/nodes/42')).toHaveLength(0)
  expect(route().get('inbound')).toBe('42')
  await act(async () => finish({ data: list([managedNode]) }))
  const dialog = await screen.findByRole('dialog')
  await waitFor(() => expect(dialog.querySelector('#edit-inbound-form')).toBeTruthy())
  await waitFor(() => expect(route().get('inbound')).toBeNull())
  expect(route().get('keep')).toBe('1')
  expect(reads('/admin/nodes/42')).toHaveLength(1)
  await act(async () => { await client.refetchQueries({ queryKey: ['private'] }) })
  expect(reads('/admin/nodes/42')).toHaveLength(1)
  expect(reads('/admin/nodes/777')).toHaveLength(0)
  expect(api.put).not.toHaveBeenCalled()
})

it.each([
  ['operator', [managedNode], server],
  ['admin', [managedNode], { ...server, capabilities: ['inbound.read'] }],
  ['admin', [], server],
])('does not open or fetch an unavailable inbound target for %s', async (role, nodes, panel) => {
  useAuthStore.setState({ role: role as 'admin' | 'operator' })
  installReads({ '/admin/nodes': list(nodes), '/admin/servers': list([panel]) })
  mount(<><NodesView /><RouteProbe next="?keep=1" /></>, ['/?inbound=42&keep=1'])
  await waitFor(() => expect(route().get('inbound')).toBeNull())
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(reads('/admin/nodes/42')).toHaveLength(0)
  expect(snack).toHaveBeenCalledWith('admin:nodes.edit_inbound_dialog.link_unavailable', 'warning')
  expect(route().get('keep')).toBe('1')
})

it('retains a deep-link request through a failed list read and opens it after an explicit successful refetch', async () => {
  installReads({ '/admin/nodes': list([managedNode]), '/admin/nodes/42': { node: managedNode, inbound } })
  const defaultGet = api.get.getMockImplementation()!
  let failed = true
  api.get.mockImplementation((url: string, ...args: unknown[]) => url === '/admin/nodes' && failed
    ? Promise.reject(new Error('list unavailable')) : defaultGet(url, ...args))
  const { client } = mount(<><NodesView /><RouteProbe next="?keep=1" /></>, ['/?inbound=42&keep=1'])
  await screen.findByText('admin:nodes.load_failed')
  expect(route().get('inbound')).toBe('42')
  expect(reads('/admin/nodes/42')).toHaveLength(0)
  failed = false
  await act(async () => { await client.refetchQueries({ queryKey: ['private'] }) })
  await screen.findByRole('dialog')
  await waitFor(() => expect(reads('/admin/nodes/42')).toHaveLength(1))
  await waitFor(() => expect(route().get('inbound')).toBeNull())
})

it('does not replace an open inbound editor when another deep link arrives', async () => {
  const secondNode = { ...managedNode, id: 43, display_name: 'second-node' }
  installReads({ '/admin/nodes': list([managedNode, secondNode]), '/admin/nodes/42': { node: managedNode, inbound }, '/admin/nodes/43': { node: secondNode, inbound } })
  mount(<><NodesView /><RouteProbe next="?inbound=43&keep=1" /></>, ['/?inbound=42&keep=1'])
  const dialog = await screen.findByRole('dialog')
  await waitFor(() => expect(dialog.querySelector('#edit-inbound-form')).toBeTruthy())
  // Simulates navigation from the address bar while an editor is open.
  fireEvent.click(screen.getByText('Navigate'))
  await waitFor(() => expect(route().get('inbound')).toBe('43'))
  expect(reads('/admin/nodes/43')).toHaveLength(0)
  expect(screen.getByRole('dialog').textContent).toContain('old-name')
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.cancel' }))
  await waitFor(() => expect(reads('/admin/nodes/43')).toHaveLength(1))
  await waitFor(() => expect(route().get('inbound')).toBeNull())
  expect(api.put).not.toHaveBeenCalled()
})

it('shows a failed fresh configuration read with retry instead of editable defaults', async () => {
  installReads({ '/admin/nodes': list([managedNode]), '/admin/nodes/42': { node: managedNode } })
  mount(<><NodesView /><RouteProbe next="?keep=1" /></>, ['/?inbound=42&keep=1'])
  const dialog = await screen.findByRole('dialog')
  await screen.findByText('admin:nodes.edit_inbound_dialog.load_failed')
  expect(dialog.querySelector('#edit-inbound-form')).toBeNull()
  expect(screen.queryByRole('button', { name: 'common:actions.ok' })).toBeNull()
  expect(screen.getByRole('button', { name: 'common:actions.retry' })).toBeTruthy()
  expect(route().get('inbound')).toBeNull()
  expect(api.put).not.toHaveBeenCalled()
})
