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

it('limits REALITY fingerprints and locks chrome when X25519MLKEM768 is enabled', async () => {
  const realityInbound = {
    ...inbound,
    stream_settings: JSON.stringify({
      network: 'tcp',
      security: 'reality',
      realitySettings: {
        target: 'example.test:443',
        serverNames: ['example.test'],
        privateKey: 'private-key',
        shortIds: ['abcd'],
        settings: { publicKey: 'public-key', fingerprint: 'firefox' },
      },
    }),
  }
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node, inbound: realityInbound, clients: [] },
  })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  const fingerprint = within(dialog).getByRole('combobox', {
    name: 'admin:nodes.create_dialog.reality_fingerprint',
  })
  expect(within(dialog).getByLabelText('admin:nodes.create_dialog.reality_min_client')
    .getAttribute('placeholder')).toBe('1.8.1')

  fireEvent.mouseDown(fingerprint)
  const options = await screen.findAllByRole('option')
  expect(options.map(option => option.textContent)).toEqual(['chrome', 'firefox', 'safari'])
  fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' })

  // The title is part of FormControlLabel, matching the larger click target
  // used by the other switches in this form.
  fireEvent.click(within(dialog).getByText('admin:nodes.create_dialog.reality_support_x25519mlkem768'))
  await waitFor(() => {
    expect(fingerprint.getAttribute('aria-disabled')).toBe('true')
    expect(fingerprint.textContent).toBe('chrome')
  })

  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(api.put).toHaveBeenCalled())
  const request = api.put.mock.calls.find(([url]) => url === '/admin/nodes/1/inbound')?.[1]
  const streamSettings = JSON.parse(request.stream_settings)
  expect(streamSettings.realitySettings.settings).toMatchObject({
    fingerprint: 'chrome',
    supportX25519MLKEM768: true,
  })
})
