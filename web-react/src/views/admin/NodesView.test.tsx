// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
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

it('reopens node metadata from the update response without reloading the stale list', async () => {
  const saved = { ...node, display_name: 'new-name' }
  installReads({ '/admin/nodes': list([node]) })
  api.put.mockResolvedValueOnce({ data: saved })
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow('new-name')
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()
})

it('shows desired and observed endpoints separately when they drift', async () => {
  const drifted = {
    ...node,
    observed_protocol: 'trojan',
    observed_port: 8443,
    endpoint_in_sync: false,
  }
  installReads({ '/admin/nodes': list([drifted]) })
  mount(<NodesView />)

  expect(await screen.findByText(/vless:443/)).toBeTruthy()
  expect(await screen.findByText(/trojan:8443/)).toBeTruthy()
})
