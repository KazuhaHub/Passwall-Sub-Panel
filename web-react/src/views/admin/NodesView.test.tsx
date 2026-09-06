// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, deferred, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'

const inbound = { id: 1, protocol: 'vless', remark: 'old-name', enable: true, port: 443, listen: '', settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"tls","tlsSettings":{"serverName":"example.test"}}', sniffing: '{}', allocate: '' }

it('uses the fresh detail node when opening the inbound editor', async () => {
  const freshNode = { ...node, flow: 'xtls-rprx-vision', cert_source: 'psp_managed', cert_id: 7 }
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node: freshNode, inbound, clients: [] },
    '/admin/certs': { certs: [{ id: 7, name: 'managed', domains: ['example.test'], status: 'active' }] },
  })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  await waitFor(() => expect(within(dialog).getByDisplayValue('psp_managed')).toBeTruthy())
  expect(within(dialog).getByDisplayValue('xtls-rprx-vision')).toBeTruthy()
})

it('reopens node metadata from the saved local row while refresh is pending', async () => {
  const saved = { ...node, display_name: 'new-name' }
  installReads({ '/admin/nodes': list([node]) })
  api.put.mockResolvedValueOnce({ data: saved })
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  const pending = deferred<{ data: unknown }>()
  const normalGet = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, ...args: unknown[]) =>
    url === '/admin/nodes' ? pending.promise : normalGet(url, ...args),
  )
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow('new-name')
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()

  await act(async () => pending.resolve({ data: list([saved]) }))
})
