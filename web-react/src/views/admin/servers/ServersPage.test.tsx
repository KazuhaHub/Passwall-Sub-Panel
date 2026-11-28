// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mountWithClient, mount, server } from './testHarness'
import { useAuthStore } from '@/stores/auth'
import { openMenuOf, openServerTab, pickRowAction } from './install/installationHarness'
import ServersPage from './ServersPage'

it('does not re-probe upstream servers when the list is merely refreshed', async () => {
  // /admin/servers/probe is a real request to the upstream panel. Its trigger
  // is the page's row-ID set, not the row data — if a plain refresh ever became
  // the trigger, enabling list polling would hammer every server on the page
  // once per cycle. This pins that separation.
  installReads({ '/admin/servers': list([server]) })
  const probes = () => api.post.mock.calls.filter(c => c[0] === '/admin/servers/probe').length

  const { client } = mountWithClient(<ServersPage />)
  await screen.findByText('server')
  await waitFor(() => expect(probes()).toBeGreaterThan(0))
  const before = probes()

  // Refetch the list. The rows are identical, so the id-set is unchanged.
  await act(async () => {
    await client.refetchQueries({ queryKey: ['private'] })
  })

  expect(probes()).toBe(before)
})

it('keeps the row menu to open, edit settings, host metrics and delete', async () => {
  const native = { ...server, panel_type: 'psp', url: 'psp://agt_1' }
  installReads({ '/admin/servers': list([native, { ...server, id: 2, name: 'upstream' }]) })
  mount(<ServersPage />)
  await openMenuOf(native as never)
  expect(screen.getAllByRole('menuitem').map(i => i.textContent)).toEqual([
    'admin:servers.action.open', 'admin:servers.action.edit_settings', 'admin:nodeMetrics.title', 'admin:servers.action.delete',
  ])
  fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
  await openMenuOf({ ...server, name: 'upstream' } as never)
  // Host metrics belong to a Passwall Node only.
  expect(screen.queryByRole('menuitem', { name: 'admin:nodeMetrics.title' })).toBeNull()
})

it('opens the server page from the row menu and from the row, and the settings tab from Edit settings', async () => {
  installReads({ '/admin/servers': list([server]) })
  const { router } = mount(<ServersPage />)
  await pickRowAction(server as never, 'admin:servers.action.open')
  await screen.findByRole('tab', { name: 'admin:servers.detail.tabs.overview' })
  expect(router.state.location.pathname).toBe('/admin/servers/1')
  expect(router.state.location.search).toBe('')
  await router.navigate('/admin/servers')
  await waitFor(() => expect(screen.queryByRole('tab')).toBeNull())
  await pickRowAction(server as never, 'admin:servers.action.edit_settings')
  await screen.findByRole('textbox', { name: /admin:servers.field.name/ })
  expect(router.state.location.search).toBe('?tab=settings')
  await router.navigate('/admin/servers')
  await waitFor(() => expect(screen.queryByRole('tab')).toBeNull())
  fireEvent.click((await screen.findByText('server')).closest('tr')!)
  await screen.findByRole('tab', { name: 'admin:servers.detail.tabs.overview' })
  expect(router.state.location.pathname).toBe('/admin/servers/1')
})

it('shows the diagnostics and install entries on the server page to an administrator only', async () => {
  const native = { ...server, panel_type: 'psp', url: 'psp://agt_1' }
  for (const role of ['admin', 'operator'] as const) {
    useAuthStore.setState({ role })
    installReads({ '/admin/servers': list([native]) })
    const { unmount } = mount(<ServersPage />)
    await openServerTab(native as never, 'monitoring')
    expect(await screen.findByRole('button', { name: 'admin:nodeMetrics.title' })).toBeTruthy()
    expect(!!screen.queryByRole('button', { name: 'admin:nodeDiagnostics.title' })).toBe(role === 'admin')
    fireEvent.click(screen.getByRole('tab', { name: 'admin:servers.detail.tabs.maintenance' }))
    await screen.findByRole('button', { name: 'admin:servers.agent_upgrade.action' })
    expect(!!screen.queryByRole('button', { name: 'admin:servers.install_reinstall.action' })).toBe(role === 'admin')
    // The settings tab stays for both.
    expect(screen.getByRole('tab', { name: 'admin:servers.detail.tabs.settings' })).toBeTruthy()
    unmount()
  }
})

const probesOf = () => api.post.mock.calls.filter(c => c[0] === '/admin/servers/probe')

it('shows the previous result when the list is shown again within the window, and sends no second probe', async () => {
  installReads({ '/admin/servers': list([server]) })
  const first = mountWithClient(<ServersPage />)
  await screen.findByText('server')
  await waitFor(() => expect(probesOf()).toHaveLength(1))
  first.unmount()
  mountWithClient(<ServersPage />, '/admin/servers', first.client)
  await screen.findByText('server')
  await act(async () => {})
  expect(probesOf()).toHaveLength(1)
})

it('probes again once the result is older than the window', async () => {
  installReads({ '/admin/servers': list([server]) })
  const first = mountWithClient(<ServersPage />)
  await waitFor(() => expect(probesOf()).toHaveLength(1))
  first.unmount()
  const before = probesOf().length
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(Date.now() + 61_000)
  mountWithClient(<ServersPage />, '/admin/servers', first.client)
  await waitFor(() => expect(probesOf().length).toBeGreaterThan(before))
  vi.useRealTimers()
})

it('forgets a server\'s result when it is saved', async () => {
  installReads({ '/admin/servers': list([server]) })
  api.put.mockResolvedValue({ data: server })
  const first = mountWithClient(<ServersPage />)
  await waitFor(() => expect(probesOf()).toHaveLength(1))
  first.router.navigate('/admin/servers/1?tab=settings')
  fireEvent.change(await screen.findByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: 'renamed' } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  const before = probesOf().length
  first.router.navigate('/admin/servers')
  await waitFor(() => expect(probesOf().length).toBeGreaterThan(before))
})
