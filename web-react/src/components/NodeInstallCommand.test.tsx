// @vitest-environment jsdom
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ThemeProvider } from '@mui/material'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { mount } from '@/test/adminSaveHarness'
import NodeInstallCommand from './NodeInstallCommand'

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
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

  // A COPY THAT RESOLVES WHILE STRICT-MODE'S SECOND EFFECT PASS IS STILL PENDING
  // MUST STILL REPORT ITS RESULT.
  //
  // StrictMode mounts, unmounts and remounts every component, and the remount's
  // effect can land AFTER a click: measured on a loaded runner, the cycle came
  // through as cleanup(intent 0) then effect(intent 1) between the click and the
  // clipboard promise resolving. The cleanup bumped `intent` unconditionally, so
  // the in-flight copy no longer matched and its feedback was dropped — the
  // button then went quiet forever, which is the "copy did nothing" bug. The
  // effect body already guards against invalidating on mount (see its comment);
  // the cleanup has to agree, or every simulated unmount cancels a real copy.
  //
  // The render is deliberately NOT wrapped in act(): that is what leaves the
  // second effect pass of StrictMode pending, so the click below starts the copy
  // the way the failing CI run did. A test that flushed the mount first cannot
  // see this at all.
  it('reports copy feedback when the clipboard resolves during a strict-mode remount', async () => {
    let finish!: (value: boolean) => void
    copy.mockImplementation(() => new Promise<boolean>(resolve => { finish = resolve }))
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    root.render(
      <StrictMode><ThemeProvider theme={theme}>
        <NodeInstallCommand command={command} expiresAt={future()} onExpired={vi.fn()} />
      </ThemeProvider></StrictMode>,
    )
    await new Promise(resolve => setTimeout(resolve, 20))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    await act(async () => finish(false))
    expect(screen.queryByRole('alert')?.textContent).toBe('admin:servers.native.copy_failed')
    expect((screen.getByRole('button', { name: 'admin:servers.native.copy_command' }) as HTMLButtonElement).disabled).toBe(false)
    act(() => root.unmount())
  })
})
