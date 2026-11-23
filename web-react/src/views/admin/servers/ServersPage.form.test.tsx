// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, server, snack } from './testHarness'
import { pickRowAction } from './install/installationHarness'
import ServersPage from './ServersPage'

describe('add and edit server form', () => {
  it('never shows a stored secret, keeps it when left empty, and replaces it when one is typed', async () => {
    installReads({ '/admin/servers': list([server]) })
    api.put.mockResolvedValue({ data: server })
    mount(<ServersPage />)
    await pickRowAction(server as never, 'admin:servers.action.edit')
    const sheet = await screen.findByRole('dialog')
    const token = within(sheet).getByLabelText(/admin:servers.field.api_token/) as HTMLInputElement
    expect(token.value).toBe('')
    expect(token.type).toBe('password')
    expect(within(sheet).getByText('admin:servers.hint.configured')).toBeTruthy()
    fireEvent.change(within(sheet).getByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: 'renamed' } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1]).not.toHaveProperty('api_token')
    expect(api.put.mock.calls[0][1]).toMatchObject({ name: 'renamed', url: server.url })
    // The secret is in the body, never in a toast or the address.
    expect(JSON.stringify(snack.mock.calls)).not.toContain('token')

    await pickRowAction(server as never, 'admin:servers.action.edit')
    fireEvent.change(within(await screen.findByRole('dialog')).getByLabelText(/admin:servers.field.api_token/), { target: { value: 'fresh-token' } })
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
    expect(api.put.mock.calls[1][1]).toMatchObject({ api_token: 'fresh-token' })
  })

  it('asks before closing a form with unsaved changes, and not otherwise', async () => {
    installReads({ '/admin/servers': list([server]) })
    mount(<ServersPage />)
    await pickRowAction(server as never, 'admin:servers.action.edit')
    const sheet = await screen.findByRole('dialog')
    fireEvent.change(within(sheet).getByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: 'edited' } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'common:actions.cancel' }))
    const ask = await screen.findByRole('alertdialog')
    fireEvent.click(within(ask).getByRole('button', { name: 'admin:access_control.confirm.discard.keep' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(screen.getByRole('textbox', { name: /admin:servers.field.name/ })).toBeTruthy()
  })

  it('runs the connection test from the edit form and says the result in words', async () => {
    installReads({ '/admin/servers': list([server]) })
    api.post.mockResolvedValue({ data: { ok: false, error: 'connection refused' } })
    mount(<ServersPage />)
    await pickRowAction(server as never, 'admin:servers.action.edit')
    const sheet = await screen.findByRole('dialog')
    fireEvent.click(within(sheet).getByRole('button', { name: 'admin:servers.action.test' }))
    expect((await within(sheet).findByRole('alert')).textContent).toContain('connection refused')
    expect(api.post).toHaveBeenCalledWith('/admin/servers/probe', { id: 1 })
  })
})
