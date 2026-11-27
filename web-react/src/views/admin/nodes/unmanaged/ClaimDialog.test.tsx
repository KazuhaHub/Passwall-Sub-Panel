// @vitest-environment jsdom
import { screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, mount } from '@/test/adminSaveHarness'
import { ClaimDialog } from './ClaimDialog'

const inbound = { PanelID: 1, PanelName: 'server', InboundID: 9, Protocol: 'vless', Port: 443, Remark: 'legacy', Enable: true, ClientCount: 2 }

it('says the user list is loading instead of showing an empty picker', async () => {
  let resolve!: (v: { data: unknown }) => void
  api.get.mockImplementation(() => new Promise(r => { resolve = r }))
  mount(<ClaimDialog inbound={inbound} onClose={vi.fn()} onClaimed={vi.fn()} />)
  // While listUsers is in flight the picker must say so: "no users" and "not loaded yet" must not look alike.
  const picker = await screen.findByPlaceholderText('admin:nodes.claim_dialog.users_loading')
  expect((picker as HTMLInputElement).disabled).toBe(true)
  resolve({ data: { items: [{ id: 2, upn: 'user@example.test', display_name: 'A User' }], total: 1, page: 1, page_size: 200 } })
  await waitFor(() => expect((screen.getByPlaceholderText('admin:nodes.claim_dialog.user') as HTMLInputElement).disabled).toBe(false))
})
