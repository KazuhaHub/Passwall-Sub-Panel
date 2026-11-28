// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, server, snack } from './testHarness'
import { pickRowAction } from './install/installationHarness'
import ServersPage from './ServersPage'

const name = () => screen.findByRole('textbox', { name: /admin:servers.field.name/ })
const save = () => screen.getByRole('button', { name: 'common:actions.save' })

describe('add and edit server form', () => {
  it('never shows a stored secret, keeps it when left empty, and replaces it when one is typed', async () => {
    installReads({ '/admin/servers': list([server]) })
    api.put.mockResolvedValue({ data: server })
    mount(<ServersPage />)
    await pickRowAction(server as never, 'admin:servers.action.edit_settings')
    const token = await screen.findByLabelText(/admin:servers.field.api_token/) as HTMLInputElement
    expect(token.value).toBe('')
    expect(token.type).toBe('password')
    expect(screen.getByText('admin:servers.hint.configured')).toBeTruthy()
    // Nothing to save until something changes.
    expect(screen.queryByRole('button', { name: 'common:actions.save' })).toBeNull()
    fireEvent.change(await name(), { target: { value: 'renamed' } })
    fireEvent.click(save())
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1]).not.toHaveProperty('api_token')
    expect(api.put.mock.calls[0][1]).toMatchObject({ name: 'renamed', url: server.url })
    // The secret is in the body, never in a toast or the address.
    expect(JSON.stringify(snack.mock.calls)).not.toContain('token')
    // Saved: the page stays, the draft is the saved record again and Save is gone.
    await waitFor(() => expect(screen.queryByRole('button', { name: 'common:actions.save' })).toBeNull())

    fireEvent.change(screen.getByLabelText(/admin:servers.field.api_token/), { target: { value: 'fresh-token' } })
    fireEvent.click(save())
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
    expect(api.put.mock.calls[1][1]).toMatchObject({ api_token: 'fresh-token' })
  })

  it('asks before leaving a form with unsaved changes, and not otherwise', async () => {
    installReads({ '/admin/servers': list([server]) })
    const { router } = mount(<ServersPage />)
    await pickRowAction(server as never, 'admin:servers.action.edit_settings')
    expect(router.state.location.pathname).toBe('/admin/servers/1')
    // An untouched form is left without a question.
    fireEvent.click(await screen.findByRole('link', { name: 'admin:servers.title' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/servers'))

    await pickRowAction(server as never, 'admin:servers.action.edit_settings')
    fireEvent.change(await name(), { target: { value: 'edited' } })
    fireEvent.click(screen.getByRole('link', { name: 'admin:servers.title' }))
    const ask = await screen.findByRole('alertdialog')
    fireEvent.click(within(ask).getByRole('button', { name: 'admin:access_control.confirm.discard.keep' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(router.state.location.pathname).toBe('/admin/servers/1')
    expect((await name() as HTMLInputElement).value).toBe('edited')
  })

  it('opens the server by its address on the page, and treats a malformed or unknown one as no such server', async () => {
    installReads({ '/admin/servers': list([server]) })
    mount(<ServersPage />, '/admin/servers/1')
    // The page opens on its overview; the form is the Settings tab, also reachable by its address.
    expect((await screen.findByRole('tab', { name: 'admin:servers.detail.tabs.overview' })).getAttribute('aria-selected')).toBe('true')
    expect(screen.queryByRole('textbox', { name: /admin:servers.field.name/ })).toBeNull()
    fireEvent.click(screen.getByRole('tab', { name: 'admin:servers.detail.tabs.settings' }))
    expect(((await name()) as HTMLInputElement).value).toBe('server')
    cleanup()
    mount(<ServersPage />, '/admin/servers/1?tab=settings')
    expect(((await name()) as HTMLInputElement).value).toBe('server')
    cleanup()
    api.get.mockClear()
    mount(<ServersPage />, '/admin/servers/abc')
    expect(await screen.findByText('admin:servers.detail.not_found')).toBeTruthy()
    expect(api.get).not.toHaveBeenCalled()
    cleanup()
    mount(<ServersPage />, '/admin/servers/99')
    expect(await screen.findByText('admin:servers.detail.not_found')).toBeTruthy()
  })

  it('runs the connection test from the page and says the result in words', async () => {
    installReads({ '/admin/servers': list([server]) })
    api.post.mockResolvedValue({ data: { ok: false, error: 'connection refused' } })
    mount(<ServersPage />, '/admin/servers/1?tab=settings')
    await name()
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.action.test' }))
    expect((await screen.findByRole('alert')).textContent).toContain('connection refused')
    expect(api.post).toHaveBeenCalledWith('/admin/servers/probe', { id: 1 })
  })

  it('saves nothing twice while a save is pending', async () => {
    installReads({ '/admin/servers': list([server]) })
    let release: (v: { data: unknown }) => void = () => {}
    api.put.mockReturnValue(new Promise(resolve => { release = resolve }))
    mount(<ServersPage />, '/admin/servers/1?tab=settings')
    fireEvent.change(await name(), { target: { value: 'renamed' } })
    fireEvent.click(save())
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    // Enter in the field submits the form again: it must not start a second save.
    fireEvent.submit((await name()).closest('form')!)
    expect(api.put).toHaveBeenCalledTimes(1)
    release({ data: { ...server, name: 'renamed' } })
    await waitFor(() => expect(screen.queryByRole('button', { name: 'common:actions.save' })).toBeNull())
  })

  it('deletes from the page after its confirmation and returns to the list', async () => {
    installReads({ '/admin/servers': list([server]), '/admin/servers/1/delete-impact': { server_id: 1, nodes: [], lines_entering: [], egress_users: [], route_users: [] } })
    const { router } = mount(<ServersPage />, '/admin/servers/1?tab=settings')
    await name()
    fireEvent.click(screen.getByRole('button', { name: /ui.shell.more/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.action.delete' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(router.state.location.pathname).toBe('/admin/servers/1')
    api.delete.mockResolvedValue({ data: {} })
    fireEvent.click(await within(dialog).findByRole('button', { name: 'admin:servers.delete.confirm' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/servers'))
    expect(api.delete).toHaveBeenCalledTimes(1)
  })
})
