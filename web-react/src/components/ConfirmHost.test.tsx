// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ConfirmHost, { confirm } from './ConfirmHost'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k: string) => ({ 'actions.cancel': '取消', 'actions.ok': '确定' }[k] ?? k) }),
}))

afterEach(cleanup)

// confirm() returns a promise the page awaits; this reads whatever it settled to.
function ask(opts: Parameters<typeof confirm>[0]) {
  const settled: { value?: boolean } = {}
  void act(() => { void confirm(opts).then(v => { settled.value = v }) })
  return settled
}

describe('ConfirmHost', () => {
  it('resolves false at once when no host is mounted', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await expect(confirm({ title: 't', message: 'm' })).resolves.toBe(false)
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('shows the title, message and default labels in an alert dialog', async () => {
    render(<ConfirmHost />)
    ask({ title: '删除节点', message: '节点 A 会被删除。\n此操作无法撤销。' })
    const dialog = await screen.findByRole('alertdialog', { name: '删除节点' })
    expect(dialog.textContent).toContain('此操作无法撤销')
    expect(screen.getByRole('button', { name: '取消' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '确定' })).toBeTruthy()
  })

  it('resolves true on the confirm button and uses custom labels', async () => {
    render(<ConfirmHost />)
    const settled = ask({ title: 't', message: 'm', confirmText: '删除节点', cancelText: '保留' })
    fireEvent.click(await screen.findByRole('button', { name: '删除节点' }))
    await waitFor(() => expect(settled.value).toBe(true))
    expect(screen.queryByRole('button', { name: '保留' })).toBeNull()
  })

  it('resolves false on the cancel button', async () => {
    render(<ConfirmHost />)
    const settled = ask({ title: 't', message: 'm' })
    fireEvent.click(await screen.findByRole('button', { name: '取消' }))
    await waitFor(() => expect(settled.value).toBe(false))
  })

  it('resolves false on Escape', async () => {
    render(<ConfirmHost />)
    const settled = ask({ title: 't', message: 'm' })
    const dialog = await screen.findByRole('alertdialog')
    fireEvent.keyDown(dialog, { key: 'Escape' })
    await waitFor(() => expect(settled.value).toBe(false))
  })

  it('resolves false on a click on the backdrop, as the previous dialog did', async () => {
    render(<ConfirmHost />)
    const settled = ask({ title: 't', message: 'm' })
    await screen.findByRole('alertdialog')
    const backdrop = document.querySelector('[data-slot="alert-dialog-overlay"]') as HTMLElement
    expect(backdrop).toBeTruthy()
    fireEvent.click(backdrop)
    await waitFor(() => expect(settled.value).toBe(false))
  })

  it('puts focus on the confirm button', async () => {
    render(<ConfirmHost />)
    ask({ title: 't', message: 'm' })
    const ok = await screen.findByRole('button', { name: '确定' })
    await waitFor(() => expect(document.activeElement).toBe(ok))
  })

  it('stacks above every MUI layer, tooltips included', async () => {
    render(<ConfirmHost />)
    ask({ title: 't', message: 'm' })
    const dialog = await screen.findByRole('alertdialog')
    // Above MUI tooltips (1500), which sit above its modals and popovers.
    expect(dialog.className).toContain('z-[1600]')
    expect(document.querySelector('[data-slot="alert-dialog-overlay"]')!.className).toContain('z-[1600]')
  })

  it('makes a MUI modal beneath it inert while open, and releases it after', async () => {
    const modal = document.createElement('div')
    modal.className = 'MuiModal-root'
    document.body.appendChild(modal)
    render(<ConfirmHost />)
    const settled = ask({ title: 't', message: 'm' })
    await screen.findByRole('alertdialog')
    expect(modal.hasAttribute('inert')).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(settled.value).toBe(false))
    expect(modal.hasAttribute('inert')).toBe(false)
    modal.remove()
  })

  it('also makes inert a MUI modal that opens while the confirm is already up', async () => {
    render(<ConfirmHost />)
    const settled = ask({ title: 't', message: 'm' })
    await screen.findByRole('alertdialog')
    const late = document.createElement('div')
    late.className = 'MuiModal-root'
    document.body.appendChild(late)
    await waitFor(() => expect(late.hasAttribute('inert')).toBe(true))
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(settled.value).toBe(false))
    expect(late.hasAttribute('inert')).toBe(false)
    late.remove()
  })

  it('keeps focus in the confirm when a MUI modal opens over it and takes focus first', async () => {
    render(<ConfirmHost />)
    ask({ title: 't', message: 'm' })
    const ok = await screen.findByRole('button', { name: '确定' })
    await waitFor(() => expect(document.activeElement).toBe(ok))
    const late = document.createElement('div')
    late.className = 'MuiModal-root'
    const inner = document.createElement('button')
    inner.tabIndex = 0
    late.appendChild(inner)
    document.body.appendChild(late)
    inner.focus()
    expect(document.activeElement).toBe(inner)
    // MUI's trap has taken focus; the modal is made inert and focus goes back to the confirm.
    await waitFor(() => expect(document.activeElement).toBe(ok))
    late.remove()
  })

  it('answers false to a confirm that a second one replaces, so no caller hangs', async () => {
    render(<ConfirmHost />)
    const first = ask({ title: 'first', message: 'm' })
    await screen.findByRole('alertdialog', { name: 'first' })
    const second = ask({ title: 'second', message: 'm' })
    await waitFor(() => expect(first.value).toBe(false))
    await screen.findByRole('alertdialog', { name: 'second' })
    fireEvent.click(screen.getByRole('button', { name: '确定' }))
    await waitFor(() => expect(second.value).toBe(true))
  })

  it('styles the confirm button as destructive only when asked', async () => {
    render(<ConfirmHost />)
    ask({ title: 't', message: 'm', destructive: true })
    const ok = await screen.findByRole('button', { name: '确定' })
    expect(ok.className).toContain('bg-destructive')
    cleanup()

    render(<ConfirmHost />)
    ask({ title: 't', message: 'm' })
    expect((await screen.findByRole('button', { name: '确定' })).className).not.toContain('bg-destructive')
  })
})
