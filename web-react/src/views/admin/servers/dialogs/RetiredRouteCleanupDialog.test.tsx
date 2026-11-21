// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Server } from '@/api/servers'
import { api, installReads, mount, server } from '../testHarness'
import RetiredRouteCleanupDialog from './RetiredRouteCleanupDialog'

const path = '/admin/servers/1/retired-route-cleanup'
const preview = { rule_tags: ['psp-vlessroute:1:8ab539e3'], revision: 'a'.repeat(64) }

async function previewed() {
  installReads({ [path]: preview })
  api.post.mockResolvedValue({ data: preview })
  mount(<RetiredRouteCleanupDialog server={server as unknown as Server} onClose={() => {}} />)
  const dialog = await screen.findByRole('dialog')
  expect(within(dialog).queryByRole('button', { name: 'admin:servers.retired_routes.cleanup' })).toBeNull()
  fireEvent.click(within(dialog).getByRole('button', { name: 'admin:servers.retired_routes.preview' }))
  await within(dialog).findByText(preview.rule_tags[0])
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.click(within(dialog).getByRole('button', { name: 'admin:servers.retired_routes.cleanup' }))
  return screen.findByRole('alertdialog')
}

describe('RetiredRouteCleanupDialog', () => {
  it('previews first, then asks with focus on Cancel and sends nothing when cancelled', async () => {
    const ask = await previewed()
    const cancel = within(ask).getByRole('button', { name: 'common:actions.cancel' })
    await waitFor(() => expect(document.activeElement).toBe(cancel))
    fireEvent.click(cancel)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(api.post).not.toHaveBeenCalled()
  })

  it('sends the confirmed revision once the removal is confirmed and says how many rules went', async () => {
    const ask = await previewed()
    fireEvent.click(within(ask).getByRole('button', { name: 'admin:servers.retired_routes.cleanup' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith(path, { confirm: true, revision: preview.revision }, { _skipErrorToast: true }))
    await screen.findByText('admin:servers.retired_routes.removed')
  })
})
