// @vitest-environment jsdom
import { act, screen, waitFor } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, installReads, list, mountWithClient, server } from '@/test/adminSaveHarness'
import ServersView from './ServersView'

it('does not re-probe upstream servers when the list is merely refreshed', async () => {
  // /admin/servers/probe is a real request to the upstream panel. Its trigger
  // is the page's row-ID set, not the row data — if a plain refresh ever became
  // the trigger, enabling list polling would hammer every server on the page
  // once per cycle. This pins that separation.
  installReads({ '/admin/servers': list([server]) })
  const probes = () => api.post.mock.calls.filter(c => c[0] === '/admin/servers/probe').length

  const { client } = mountWithClient(<ServersView />)
  await screen.findByText('server')
  await waitFor(() => expect(probes()).toBeGreaterThan(0))
  const before = probes()

  // Refetch the list. The rows are identical, so the id-set is unchanged.
  await act(async () => {
    await client.refetchQueries({ queryKey: ['private'] })
  })

  expect(probes()).toBe(before)
})
