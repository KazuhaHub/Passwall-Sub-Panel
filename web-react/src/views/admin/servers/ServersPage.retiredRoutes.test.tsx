// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, server } from './testHarness'
import { openMenuOf, pickRowAction } from './install/installationHarness'
import ServersPage from './ServersPage'

const path = '/admin/servers/1/retired-route-cleanup'
const preview = { rule_tags: ['psp-vlessroute:1:8ab539e3-2f2d-4fea-93ee-418321c3f887'], revision: 'a'.repeat(64) }

describe('ServersPage retired route cleanup', () => {
  it('previews the 3X-UI rules before sending a confirmed update', async () => {
    installReads({ '/admin/servers': list([{ ...server, panel_type: '3xui' }]), [path]: preview })
    api.post.mockResolvedValue({ data: preview })
    mount(<ServersPage />)
    await pickRowAction(server as never, 'admin:servers.retired_routes.open')
    const dialog = await screen.findByRole('dialog')
    const cleanup = { name: 'admin:servers.retired_routes.cleanup' }
    expect(within(dialog).queryByRole('button', cleanup)).toBeNull()
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:servers.retired_routes.preview' }))
    await within(dialog).findByText(preview.rule_tags[0])
    expect(api.post).not.toHaveBeenCalledWith(path, expect.anything(), expect.anything())
    fireEvent.click(within(dialog).getByRole('button', cleanup))
    // It names the server and asks before removing anything, with focus on Cancel.
    const confirmation = await screen.findByRole('alertdialog')
    const cancel = within(confirmation).getByRole('button', { name: 'common:actions.cancel' })
    await waitFor(() => expect(document.activeElement).toBe(cancel))
    fireEvent.click(within(confirmation).getByRole('button', cleanup))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith(path, { confirm: true, revision: preview.revision }, { _skipErrorToast: true }))
  })

  it('does not offer cleanup for Passwall Node', async () => {
    installReads({ '/admin/servers': list([{ ...server, panel_type: 'psp' }]) })
    mount(<ServersPage />)
    await openMenuOf(server as never)
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.retired_routes.open' })).toBeNull()
  })
})
