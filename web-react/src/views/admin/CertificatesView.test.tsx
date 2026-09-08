// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, editRow, installReads, mount } from '@/test/adminSaveHarness'
import CertificatesView from './CertificatesView'

const cert = { id: 1, name: 'old-name', domains: ['example.test'], acme_account_id: 1, dns_credential_id: 1, auto_renew: true, status: 'active' }
const credential = { id: 1, name: 'old-name', provider: 'custom', keys: ['TOKEN'] }
const account = { id: 1, name: 'old-name', email: 'admin@example.test', directory: 'https://acme.test/directory', key_type: 'EC256', eab_key_id: '', has_eab_hmac: true }
const reads = {
  '/admin/certs': { certs: [cert] },
  '/admin/dns-credentials': { credentials: [credential] },
  '/admin/acme-accounts': { accounts: [account] },
}

for (const testCase of [
  { name: 'certificate', tab: 0, url: '/admin/certs/1', dataKey: 'cert', row: cert },
  { name: 'DNS credential', tab: 1, url: '/admin/dns-credentials/1', dataKey: 'credential', row: credential },
  { name: 'ACME account', tab: 2, url: '/admin/acme-accounts/1', dataKey: 'account', row: account },
] as const) {
  describe(testCase.name, () => {
    it('reopens with the update response without reloading the stale list', async () => {
      installReads(reads)
      mount(<CertificatesView />)
      if (testCase.tab) fireEvent.click((await screen.findAllByRole('tab'))[testCase.tab])
      const dialog = await editRow()
      fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })
      const saved = { ...testCase.row, name: 'new-name' }
      api.put.mockImplementationOnce(async (url: string) => {
        expect(url).toBe(testCase.url)
        return { data: { [testCase.dataKey]: saved } }
      })

      fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.save' }))

      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      const reopened = await editRow('new-name')
      expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()
    })
  })
}
