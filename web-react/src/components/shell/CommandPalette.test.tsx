// @vitest-environment jsdom
import { useState } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { TFunction } from 'i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

import { translate } from '@/test/i18nMock'
import type { Role } from '@/api/types'
import { ADMIN_NAV } from './adminNav'
import { CommandPalette, anotherDialogIsOpen, useCommandPaletteHotkey } from './CommandPalette'
import { matchesQuery, navigationProvider, type CommandProvider } from './commands'

beforeEach(() => {
  // cmdk measures and scrolls; jsdom has neither.
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as never
  Element.prototype.scrollIntoView = vi.fn()
})
afterEach(cleanup)

const t = ((k: string) => translate('nav', k)) as unknown as TFunction
const nav = (role: Role | '') => navigationProvider(ADMIN_NAV, role, t, '页面')

function Harness({ providers, role = 'admin', navigate = () => {} }: { providers: CommandProvider[]; role?: Role; navigate?: (to: string) => void }) {
  const [open, setOpen] = useState(false)
  useCommandPaletteHotkey(open, setOpen)
  return (
    <>
      <button onClick={() => setOpen(true)}>opener</button>
      <input aria-label="field" />
      <CommandPalette open={open} onOpenChange={setOpen} providers={providers} navigate={navigate} role={role} />
    </>
  )
}

const press = (init: KeyboardEventInit = {}) => act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true, ...init })) })
const optionNames = () => screen.getAllByRole('option').map(o => o.textContent)

describe('navigation provider', () => {
  it('offers exactly the pages the sidebar shows the role', () => {
    const ctx = { navigate() {}, close() {}, role: 'operator' as const }
    const ids = (nav('operator').search('', ctx, new AbortController().signal) as { to?: string }[]).map(e => e.to)
    expect(ids).toContain('/admin/users')
    expect(ids).not.toContain('/admin/settings')
    expect(ids).not.toContain('/admin/risk')
    expect(ids).toHaveLength(10)
  })

  it('matches by label or by route, every word', () => {
    expect(matchesQuery('同步 任务', '同步任务')).toBe(true)
    expect(matchesQuery('sync-tasks', '同步任务', '/admin/sync-tasks')).toBe(true)
    expect(matchesQuery('nope', '同步任务', '/admin/sync-tasks')).toBe(false)
    expect(matchesQuery('   ', 'x')).toBe(true)
  })
})

describe('CommandPalette', () => {
  it('opens on Ctrl+K and lists every page the role may open', async () => {
    render(<Harness providers={[nav('admin')]} />)
    expect(screen.queryByRole('dialog')).toBeNull()
    press()
    await screen.findByRole('dialog')
    expect(optionNames()).toHaveLength(18)
    expect(screen.getByText('页面')).toBeTruthy()
  })

  it('also opens on Cmd+K and from a button, and Ctrl+K closes it again', async () => {
    render(<Harness providers={[nav('admin')]} />)
    press({ ctrlKey: false, metaKey: true })
    await screen.findByRole('dialog')
    press()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    fireEvent.click(screen.getByText('opener'))
    await screen.findByRole('dialog')
  })

  it('does not offer a page the role cannot see', async () => {
    render(<Harness providers={[nav('operator')]} role="operator" />)
    press()
    await screen.findByRole('dialog')
    expect(optionNames()).not.toContain('系统设置')
    expect(optionNames()).toContain('用户管理')
  })

  it('filters as you type, and choosing opens the route and closes the palette', async () => {
    const navigate = vi.fn()
    render(<Harness providers={[nav('admin')]} navigate={navigate} />)
    press()
    const input = await screen.findByRole('combobox')
    fireEvent.change(input, { target: { value: '同步' } })
    await waitFor(() => expect(optionNames()).toEqual(['同步任务']))
    fireEvent.click(screen.getAllByRole('option')[0])
    expect(navigate).toHaveBeenCalledWith('/admin/sync-tasks')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('says so when nothing matches', async () => {
    render(<Harness providers={[nav('admin')]} />)
    press()
    fireEvent.change(await screen.findByRole('combobox'), { target: { value: 'zzzz' } })
    expect(await screen.findByText('没有匹配的结果')).toBeTruthy()
  })

  it('restores focus to what had it before it opened', async () => {
    render(<Harness providers={[nav('admin')]} />)
    const field = screen.getByLabelText('field')
    field.focus()
    press()
    await screen.findByRole('dialog')
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(field))
  })
})

describe('another dialog open', () => {
  it('keeps the palette closed while a dialog is on screen', async () => {
    render(<Harness providers={[nav('admin')]} />)
    const other = document.createElement('div')
    other.setAttribute('role', 'dialog')
    document.body.appendChild(other)
    expect(anotherDialogIsOpen()).toBe(true)
    press()
    await new Promise(r => setTimeout(r, 30))
    expect(document.querySelectorAll('[data-slot="command"]')).toHaveLength(0)
    other.remove()
    expect(anotherDialogIsOpen()).toBe(false)
    press()
    await screen.findByRole('combobox')
  })

  it('does not take Ctrl+Shift+K or Ctrl+Alt+K', async () => {
    render(<Harness providers={[nav('admin')]} />)
    press({ shiftKey: true })
    press({ altKey: true })
    await new Promise(r => setTimeout(r, 30))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})

describe('the hotkey and the key it swallows', () => {
  const fire = (target: EventTarget, init: KeyboardEventInit) => {
    const e = new KeyboardEvent('keydown', { key: 'k', bubbles: true, cancelable: true, ...init })
    act(() => { target.dispatchEvent(e) })
    return e
  }
  const setPlatform = (p: string) => Object.defineProperty(navigator, 'platform', { value: p, configurable: true })
  afterEach(() => setPlatform(''))

  it('swallows the key only when it opens or closes the palette', async () => {
    render(<Harness providers={[nav('admin')]} />)
    expect(fire(document.body, { ctrlKey: true }).defaultPrevented).toBe(true)
    await screen.findByRole('dialog')
    expect(fire(screen.getByRole('combobox'), { ctrlKey: true }).defaultPrevented).toBe(true)
  })

  it('does not swallow it when another dialog keeps the palette closed', () => {
    render(<Harness providers={[nav('admin')]} />)
    const other = document.createElement('div')
    other.setAttribute('role', 'dialog')
    document.body.appendChild(other)
    expect(fire(document.body, { ctrlKey: true }).defaultPrevented).toBe(false)
    other.remove()
  })

  it('leaves Ctrl+K to a text field off macOS, and opens from it on ⌘K', async () => {
    render(<Harness providers={[nav('admin')]} />)
    const field = screen.getByLabelText('field')
    expect(fire(field, { ctrlKey: true }).defaultPrevented).toBe(false)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(fire(field, { metaKey: true }).defaultPrevented).toBe(true)
    await screen.findByRole('dialog')
  })

  it('on a Mac, Ctrl+K is not the shortcut (it is delete-to-end-of-line) but ⌘K is', async () => {
    setPlatform('MacIntel')
    render(<Harness providers={[nav('admin')]} />)
    expect(fire(document.body, { ctrlKey: true }).defaultPrevented).toBe(false)
    expect(screen.queryByRole('dialog')).toBeNull()
    fire(document.body, { metaKey: true })
    await screen.findByRole('dialog')
  })

  it('still opens on Ctrl+K while a checkbox, radio, button or select has focus (they take no text)', async () => {
    render(<Harness providers={[nav('admin')]} />)
    for (const make of [
      () => Object.assign(document.createElement('input'), { type: 'checkbox' }),
      () => Object.assign(document.createElement('input'), { type: 'radio' }),
      () => Object.assign(document.createElement('input'), { type: 'button' }),
      () => document.createElement('select'),
    ]) {
      const el = make()
      document.body.appendChild(el)
      expect(fire(el, { ctrlKey: true }).defaultPrevented).toBe(true)
      await screen.findByRole('dialog')
      fire(screen.getByRole('combobox'), { ctrlKey: true })
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      el.remove()
    }
  })

  it('treats search, email, password and number inputs as typing', () => {
    render(<Harness providers={[nav('admin')]} />)
    for (const type of ['text', 'search', 'email', 'password', 'number', 'url']) {
      const el = Object.assign(document.createElement('input'), { type })
      document.body.appendChild(el)
      expect(fire(el, { ctrlKey: true }).defaultPrevented, type).toBe(false)
      el.remove()
    }
  })

  it('sees a text field inside a shadow root as typing, through the composed path', () => {
    render(<Harness providers={[nav('admin')]} />)
    const host = document.createElement('div')
    document.body.appendChild(host)
    const input = document.createElement('input')
    host.attachShadow({ mode: 'open' }).appendChild(input)
    const e = new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true, composed: true, cancelable: true })
    act(() => { input.dispatchEvent(e) })
    expect(e.defaultPrevented).toBe(false)
    expect(screen.queryByRole('dialog')).toBeNull()
    host.remove()
  })

  it('does not open during IME composition', () => {
    render(<Harness providers={[nav('admin')]} />)
    expect(fire(document.body, { metaKey: true, isComposing: true }).defaultPrevented).toBe(false)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('never fires inside the code editor, on either key', () => {
    render(<Harness providers={[nav('admin')]} />)
    const editor = document.createElement('div')
    editor.className = 'cm-editor'
    const inner = document.createElement('div')
    editor.appendChild(inner)
    document.body.appendChild(editor)
    expect(fire(inner, { metaKey: true }).defaultPrevented).toBe(false)
    expect(fire(inner, { ctrlKey: true }).defaultPrevented).toBe(false)
    expect(screen.queryByRole('dialog')).toBeNull()
    editor.remove()
  })
})

describe('pluggable providers', () => {
  const users: CommandProvider = {
    id: 'users', heading: '用户', debounceMs: 20, minQueryLength: 2, order: 10,
    search: async q => [{ id: 'u7', label: `用户 ${q}`, description: 'alice@example.com', to: '/admin/users?user=7' }],
  }

  it('shows a provider’s results under its own heading, after the navigation group', async () => {
    const navigate = vi.fn()
    render(<Harness providers={[users, nav('admin')]} navigate={navigate} />)
    press()
    const input = await screen.findByRole('combobox')
    // Below minQueryLength the provider contributes nothing.
    fireEvent.change(input, { target: { value: 'a' } })
    await new Promise(r => setTimeout(r, 60))
    expect(screen.queryByText('用户')).toBeNull()
    fireEvent.change(input, { target: { value: 'ali' } })
    const hit = await screen.findByText('用户 ali')
    expect(screen.getByText('alice@example.com')).toBeTruthy()
    fireEvent.click(hit.closest('[role="option"]') as HTMLElement)
    expect(navigate).toHaveBeenCalledWith('/admin/users?user=7')
  })

  it('runs an action entry with the context instead of navigating', async () => {
    const run = vi.fn()
    const actions: CommandProvider = { id: 'a', heading: '操作', search: () => [{ id: 'x', label: '清空缓存', run }] }
    render(<Harness providers={[actions]} />)
    press()
    fireEvent.click(await screen.findByText('清空缓存'))
    expect(run).toHaveBeenCalledWith(expect.objectContaining({ role: 'admin', navigate: expect.any(Function), close: expect.any(Function) }))
  })

  it('drops a slow answer that a newer query has replaced', async () => {
    let release!: (v: { id: string; label: string }[]) => void
    const slow: CommandProvider = {
      id: 's', heading: '慢', minQueryLength: 1,
      search: q => q === 'ab'
        ? new Promise(r => { release = r })
        : [{ id: 'fast', label: '快的结果' }],
    }
    render(<Harness providers={[slow]} />)
    press()
    const input = await screen.findByRole('combobox')
    fireEvent.change(input, { target: { value: 'ab' } })
    fireEvent.change(input, { target: { value: 'abc' } })
    await screen.findByText('快的结果')
    await act(async () => { release([{ id: 'late', label: '迟到的结果' }]) })
    expect(screen.queryByText('迟到的结果')).toBeNull()
  })

  it('survives a provider that throws or rejects', async () => {
    const bad: CommandProvider = { id: 'b', heading: '坏', search: () => { throw new Error('boom') } }
    const rejecting: CommandProvider = { id: 'r', heading: '拒', search: () => Promise.reject(new Error('no')) }
    render(<Harness providers={[bad, rejecting, nav('admin')]} />)
    press()
    await screen.findByRole('combobox')
    expect(optionNames()).toHaveLength(18)
  })
})
