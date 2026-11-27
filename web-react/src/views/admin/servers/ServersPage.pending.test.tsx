// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mount, server, snack } from './testHarness'
import { pickRowAction } from './install/installationHarness'
import { nativeServer, provisioning, waiting } from './install/installationHarness'
import ServersPage from './ServersPage'
import { NativeInstallationPage } from './install/NativeInstallationPage'

/**
 * SLOW-NETWORK FEEDBACK: on a 3-10s link, a row action that only disables its
 * icon after the fact reads as dead. These pin that the action button shows
 * its own busy state for the whole round trip and swallows a duplicate click,
 * exactly like AsyncButton.test.tsx pins the primitive itself.
 */

function deferred<T = void>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}


describe('ServersPage row delete', () => {
  it('opens the delete dialog, which reads what is attached first, and sends no delete until it is confirmed there', async () => {
    installReads({ '/admin/servers': list([server]), '/admin/servers/1/delete-impact': { server_id: 1, nodes: [], lines_entering: [], egress_users: [], route_users: [] } })
    const gate = deferred<{ data: unknown }>()
    api.delete.mockReturnValue(gate.promise)
    mount(<ServersPage />)

    await pickRowAction(server as never, 'admin:servers.action.delete')
    const dialog = await screen.findByRole('alertdialog')
    const confirmButton = await within(dialog).findByRole('button', { name: 'admin:servers.delete.confirm' })
    expect(api.delete).not.toHaveBeenCalled()

    fireEvent.click(confirmButton)
    // The confirm shows its own wait and a second click cannot send a second delete.
    await waitFor(() => expect(confirmButton.getAttribute('aria-busy')).toBe('true'))
    fireEvent.click(confirmButton)
    expect(api.delete).toHaveBeenCalledTimes(1)

    gate.resolve({ data: {} })
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:servers.toast.deleted', 'success'))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
  })
})

describe('ServersPage native installation credential import', () => {
  it('shows a spinner on Import credential for the whole request, not just disabled', async () => {
    // The legacy-credential prompt only appears when the installation read comes
    // back 409 node_credential_unavailable — same trigger as the existing
    // "imports the original credential" case in ServersView.installationCommand.test.tsx.
    let imported = false
    api.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/node-agent-status')) return { data: waiting }
      if (!imported) throw { response: { status: 409, data: { code: 'node_credential_unavailable', error: 'unavailable' } } }
      return { data: provisioning }
    })
    const gate = deferred<{ data: unknown }>()
    api.post.mockImplementation(() => gate.promise)
    mount(<NativeInstallationPage server={nativeServer} initialProvisioning={null} onRotate={vi.fn()} />)

    await screen.findByText('admin:servers.native.legacy_credential_hint')
    fireEvent.change(screen.getByLabelText('admin:servers.native.old_credential'), { target: { value: provisioning.credential } })
    const importButton = screen.getByRole('button', { name: 'admin:servers.native.import_credential' })

    fireEvent.click(importButton)

    await waitFor(() => expect((importButton as HTMLButtonElement).disabled).toBe(true))
    expect(importButton.getAttribute('aria-busy')).toBe('true')
    // The field beside it must stay locked too, or the operator could edit the
    // value that is already mid-flight.
    expect((screen.getByLabelText('admin:servers.native.old_credential') as HTMLInputElement).disabled).toBe(true)

    imported = true
    gate.resolve({ data: { ok: true } })
    await waitFor(() => expect(screen.queryByLabelText('admin:servers.native.old_credential')).toBeNull())
  })
})
