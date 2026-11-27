// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mountWithClient, mount, server } from './testHarness'
import { useAuthStore } from '@/stores/auth'
import { openMenuOf } from './install/installationHarness'
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

it('shows the diagnostics and install entries to an administrator only', async () => {
  const native = { ...server, panel_type: 'psp', url: 'psp://agt_1' }
  for (const role of ['admin', 'operator'] as const) {
    useAuthStore.setState({ role })
    installReads({ '/admin/servers': list([native]) })
    const { unmount } = mount(<ServersPage />)
    await openMenuOf(native as never)
    expect(!!screen.queryByRole('menuitem', { name: 'admin:nodeDiagnostics.title' })).toBe(role === 'admin')
    expect(!!screen.queryByRole('menuitem', { name: 'admin:servers.install_reinstall.action' })).toBe(role === 'admin')
    // Everything else in the menu stays for both.
    expect(screen.getByRole('menuitem', { name: 'admin:servers.action.edit' })).toBeTruthy()
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
  first.router.navigate('/admin/servers/1')
  fireEvent.change(await screen.findByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: 'renamed' } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  const before = probesOf().length
  first.router.navigate('/admin/servers')
  await waitFor(() => expect(probesOf().length).toBeGreaterThan(before))
})
