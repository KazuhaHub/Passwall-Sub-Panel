// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@/test/adminSaveHarness'
import NodeInstallCommand from './NodeInstallCommand'

const copy = vi.hoisted(() => vi.fn())
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))
const command = 'curl -fsSL https://panel.test/bootstrap/one-private-ticket -o /tmp/node-installer'
const future = () => new Date(Date.now() + 15 * 60_000).toISOString()
beforeEach(() => { copy.mockResolvedValue(true) })

describe('private one-line node command', () => {
  it('uses a labeled read-only single-line input and gives successful copy feedback', async () => {
    mount(<NodeInstallCommand command={command} expiresAt={future()} onExpired={vi.fn()} />)
    const field = screen.getByRole('textbox', { name: 'admin:servers.native.install_command' }) as HTMLInputElement
    expect(field.tagName).toBe('INPUT')
    expect(field.readOnly).toBe(true)
    expect(field.value).toBe(command)
    expect(screen.getByText('admin:servers.native.command_safety')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    await screen.findByText('admin:servers.native.copied')
    expect(copy).toHaveBeenCalledWith(command)
  })

  it.each(['false', 'throw'])('shows a manual next action when clipboard copying returns %s', async outcome => {
    if (outcome === 'false') copy.mockResolvedValue(false)
    else copy.mockRejectedValue(new Error('clipboard denied'))
    mount(<NodeInstallCommand command={command} expiresAt={future()} onExpired={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    expect((await screen.findByRole('alert')).textContent).toBe('admin:servers.native.copy_failed')
    expect(screen.getByRole('textbox', { name: 'admin:servers.native.install_command' })).toBeTruthy()
    expect((screen.getByRole('button', { name: 'admin:servers.native.copy_command' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('does not copy expired material and does not show feedback from a previous command intent', async () => {
    const expired = vi.fn()
    const view = mount(<NodeInstallCommand command={command} expiresAt={new Date(Date.now() - 1000).toISOString()} onExpired={expired} />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    expect(expired).toHaveBeenCalledOnce()
    expect(copy).not.toHaveBeenCalled()
    let finish!: (value: boolean) => void
    copy.mockImplementation(() => new Promise<boolean>(resolve => { finish = resolve }))
    view.rerender(<NodeInstallCommand command={command} expiresAt={future()} onExpired={expired} />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    await waitFor(() => expect(copy).toHaveBeenCalledOnce())
    view.rerender(<NodeInstallCommand command="another-private-command" expiresAt={future()} onExpired={expired} />)
    await act(async () => finish(true))
    expect(screen.queryByText('admin:servers.native.copied')).toBeNull()
    expect(screen.queryByText('admin:servers.native.copy_failed')).toBeNull()
  })
})
