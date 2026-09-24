// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mount, server, snack } from '@/test/adminSaveHarness'
import { nativeServer, provisioning, waiting } from '@/test/installationHarness'
import ConfirmHost from '@/components/ConfirmHost'
import ServersView, { NativeInstallationDialog } from './ServersView'

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


describe('ServersView row delete button', () => {
  it('shows busy on the delete icon for the whole delete request and ignores a second click', async () => {
    installReads({ '/admin/servers': list([server]) })
    const gate = deferred<{ data: unknown }>()
    api.delete.mockReturnValue(gate.promise)
    mount(<><ConfirmHost /><ServersView /></>)

    const deleteButton = await screen.findByRole('button', { name: 'admin:servers.action.delete' })
    fireEvent.click(deleteButton)

    const confirmDialog = await screen.findByRole('dialog')
    fireEvent.click(within(confirmDialog).getByRole('button', { name: 'admin:servers.action.delete' }))

    // The confirm dialog's exit transition keeps it in the DOM for a moment,
    // and MUI marks the rest of the page aria-hidden while any modal is
    // mounted — wait for it to actually leave before reading the row button.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(deleteButton.getAttribute('aria-busy')).toBe('true'))
    expect((deleteButton as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByRole('progressbar')).toBeTruthy()

    // A second click while the request is in flight must not fire another delete.
    fireEvent.click(deleteButton)
    expect(api.delete).toHaveBeenCalledTimes(1)

    gate.resolve({ data: {} })
    await waitFor(() => expect(deleteButton.getAttribute('aria-busy')).toBeNull())
    expect(snack).toHaveBeenCalledWith('admin:servers.toast.deleted', 'success')
  })
})

describe('ServersView native installation credential import', () => {
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
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={null} onClose={vi.fn()} onRotate={vi.fn()} />)

    await screen.findByText('admin:servers.native.legacy_credential_hint')
    fireEvent.change(screen.getByLabelText('admin:servers.native.old_credential'), { target: { value: provisioning.credential } })
    const importButton = screen.getByRole('button', { name: 'admin:servers.native.import_credential' })

    fireEvent.click(importButton)

    await waitFor(() => expect((importButton as HTMLButtonElement).disabled).toBe(true))
    expect(screen.getByRole('progressbar')).toBeTruthy()
    // The field beside it must stay locked too, or the operator could edit the
    // value that is already mid-flight.
    expect((screen.getByLabelText('admin:servers.native.old_credential') as HTMLInputElement).disabled).toBe(true)

    imported = true
    gate.resolve({ data: { ok: true } })
    await waitFor(() => expect(screen.queryByLabelText('admin:servers.native.old_credential')).toBeNull())
  })
})
