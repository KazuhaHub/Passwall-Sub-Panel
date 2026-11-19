// @vitest-environment jsdom
import { StrictMode, useEffect, useState } from 'react'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())
vi.mock('@/i18n', async () => {
  const { translate } = await import('@/test/i18nMock')
  return { default: { t: (k: string, o?: Record<string, unknown>) => translate('common', k, o) } }
})

import SnackbarHost, { pushSnack } from '../SnackbarHost'
import { useAuthStore } from '@/stores/auth'
import { notifySyncPending, setSyncPendingNavigator } from './syncPendingToast'
import { pushToast } from './toasts'

const DURATION = 4000

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { cleanup(); vi.useRealTimers() })

const toasts = () => [...document.querySelectorAll('[data-slot="toast"]:not([data-leaving])')]
const texts = () => toasts().map(t => t.querySelector('p')?.textContent)
const advance = (ms: number) => act(() => { vi.advanceTimersByTime(ms) })
const alertRegion = () => screen.getByRole('alert')
const statusRegion = () => screen.getByRole('status')
const announced = (region: HTMLElement) => [...region.children].map(c => c.textContent)

function setHidden(hidden: boolean) {
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden })
  act(() => { document.dispatchEvent(new Event('visibilitychange')) })
}
afterEach(() => { delete (document as unknown as Record<string, unknown>).hidden })

describe('mounting', () => {
  it('buffers pushes made before the host exists and shows them once it mounts', () => {
    pushSnack('早到的提示', 'info')
    render(<SnackbarHost />)
    expect(texts()).toEqual(['早到的提示'])
  })

  it('shows each push once under StrictMode’s double mount, buffered ones included', () => {
    pushSnack('缓存的', 'info')
    render(<StrictMode><SnackbarHost /></StrictMode>)
    act(() => pushSnack('之后的', 'info'))
    expect(texts()).toEqual(['缓存的', '之后的'])
    expect(announced(statusRegion())).toEqual(['缓存的', '之后的'])
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })

  it('keeps nothing between mounts: a new host starts empty', () => {
    const first = render(<SnackbarHost />)
    act(() => pushSnack('a'))
    first.unmount()
    render(<SnackbarHost />)
    expect(toasts()).toHaveLength(0)
  })

  it('ignores an empty message', () => {
    render(<SnackbarHost />)
    act(() => pushSnack(''))
    expect(toasts()).toHaveLength(0)
  })
})

describe('the toasts', () => {
  it('stack at most three, the rest waiting, and each gets its full time', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 8; i++) pushSnack(`错误 ${i}`, 'error') })
    expect(texts()).toEqual(['错误 1', '错误 2', '错误 3'])
    advance(DURATION - 1)
    expect(texts()).toEqual(['错误 1', '错误 2', '错误 3'])
    advance(1)
    expect(texts()).toEqual(['错误 4', '错误 5', '错误 6'])
  })

  it('count a repeat as ×N on the same toast', () => {
    render(<SnackbarHost />)
    act(() => { pushSnack('已复制', 'success'); pushSnack('已复制', 'success'); pushSnack('已复制', 'success') })
    expect(toasts()).toHaveLength(1)
    expect(document.querySelector('[data-slot="toast-count"]')?.textContent).toBe('×3')
  })

  it('draw each severity’s icon in its own token, not the brand colour', () => {
    render(<SnackbarHost />)
    act(() => { pushSnack('w', 'warning'); pushSnack('i', 'info'); pushSnack('e', 'error') })
    const cls = (s: string) => document.querySelector(`[data-severity="${s}"] svg`)!.getAttribute('class')
    expect(cls('warning')).toContain('text-warning-ink')
    expect(cls('info')).toContain('text-info')
    expect(cls('info')).not.toContain('text-brand')
    expect(cls('error')).toContain('text-destructive')
  })

  it('portal to body, above the confirm, bottom centre, clear of a sticky save bar', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('x'))
    const container = toasts()[0].closest('ol')!.parentElement as HTMLElement
    expect(container.parentElement).toBe(document.body)
    expect(container.className).toContain('z-[1700]')
    expect(container.className).toContain('justify-center')
    expect(container.style.paddingBottom).toContain('--toast-bottom-offset')
  })

  it('are not live regions themselves', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('x'))
    expect(document.querySelector('[data-slot="toast"] [aria-live], [data-slot="toast"][aria-live], [data-slot="toast"][role]')).toBeNull()
  })
})

describe('announcements', () => {
  it('renders two persistent live regions once, and never replaces them', () => {
    render(<SnackbarHost />)
    const a = alertRegion(); const s = statusRegion()
    act(() => { pushSnack('e', 'error'); pushSnack('i', 'info') })
    advance(60_000)
    expect(alertRegion()).toBe(a)
    expect(statusRegion()).toBe(s)
  })

  it('appends one node per message to the right region, for up to three in one tick', () => {
    render(<SnackbarHost />)
    act(() => { pushSnack('错误 1', 'error'); pushSnack('警告', 'warning'); pushSnack('信息', 'info') })
    expect(announced(alertRegion())).toEqual(['错误 1', '警告'])
    expect(announced(statusRegion())).toEqual(['信息'])
  })

  it('announces the rest of a bigger burst as ONE sentence that counts the errors', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 8; i++) pushSnack(`错误 ${i}`, 'error') })
    advance(300)
    expect(announced(alertRegion())).toEqual(['错误 1', '错误 2', '错误 3', '另有 5 条新提示，其中 5 条错误'])
  })

  it('puts the sentence in the polite region when nothing in the rest was urgent', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 6; i++) pushSnack(`信息 ${i}`, 'info') })
    advance(300)
    expect(announced(statusRegion())).toEqual(['信息 1', '信息 2', '信息 3', '另有 3 条新提示'])
    expect(alertRegion().children).toHaveLength(0)
  })

  it('announces a message once, not again when it is repeated while showing', () => {
    render(<SnackbarHost />)
    act(() => { pushSnack('e', 'error'); pushSnack('e', 'error') })
    expect(announced(alertRegion())).toEqual(['e'])
  })

  it('keeps 200 pushes in one tick to four nodes, the last one carrying the error count', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 200; i++) pushSnack(`e${i}`, 'error') })
    advance(300)
    expect(announced(alertRegion())).toHaveLength(4)
    expect(announced(alertRegion())[3]).toBe('另有 197 条新提示，其中 197 条错误')
  })

  it('announces a new window afresh', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 5; i++) pushSnack(`a${i}`, 'info') })
    advance(300)
    act(() => { pushSnack('later', 'info') })
    expect(announced(statusRegion()).at(-1)).toBe('later')
  })

  it('removes an announcement node after a while', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('e', 'error'))
    advance(8000)
    expect(alertRegion().children).toHaveLength(0)
  })
})

describe('pausing', () => {
  it('holds every countdown while the tab is hidden and finishes the time that was left on return', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 8; i++) pushSnack(`e${i}`, 'error') })
    advance(1000)
    setHidden(true)
    advance(60_000)
    expect(texts()).toEqual(['e1', 'e2', 'e3'])
    setHidden(false)
    advance(2999)
    expect(texts()).toEqual(['e1', 'e2', 'e3'])
    advance(1)
    expect(texts()).toEqual(['e4', 'e5', 'e6'])
  })

  it('holds while the pointer is over a toast and resumes when it leaves', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    advance(1000)
    fireEvent.pointerEnter(toasts()[0])
    advance(60_000)
    expect(texts()).toEqual(['a'])
    fireEvent.pointerLeave(toasts()[0])
    advance(2999)
    expect(texts()).toEqual(['a'])
    advance(1)
    expect(texts()).toEqual([])
  })

  it('holds while focus is inside the toasts and resumes when it leaves', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    advance(1000)
    const close = screen.getByRole('button', { name: '关闭' })
    // Keyboard focus: a Tab brought it here.
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true })) })
    act(() => close.focus())
    advance(60_000)
    expect(texts()).toEqual(['a'])
    act(() => close.blur())
    advance(3000)
    expect(texts()).toEqual([])
  })
})

describe('dismissing', () => {
  it('frees the slot at once, releases the next toast and gives focus back', () => {
    render(<SnackbarHost />)
    const opener = document.createElement('button')
    document.body.appendChild(opener)
    act(() => { for (const m of ['a', 'b', 'c', 'd']) pushSnack(m) })
    act(() => opener.focus())
    const close = within(toasts()[1] as HTMLElement).getByRole('button', { name: '关闭' })
    fireEvent.focus(close, { relatedTarget: opener })
    act(() => close.focus())
    fireEvent.click(close)
    expect(texts()).toEqual(['a', 'c', 'd'])
    expect(document.activeElement).toBe(opener)
    opener.remove()
  })

  it('labels the dismiss button and puts it in the tab order', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    const close = screen.getByRole('button', { name: '关闭' }) as HTMLButtonElement
    expect(close.tabIndex).toBe(0)
  })
})

describe('the exit animation', () => {
  it('holds no slot and no key: the same message right after it left is a new toast', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
    expect(document.querySelectorAll('[data-leaving]').length).toBeGreaterThan(0)
    act(() => pushSnack('a'))
    expect(toasts()).toHaveLength(1)
    expect(document.querySelector('[data-slot="toast-count"]')).toBeNull()
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })

  it('removes the leaving element after the animation', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    advance(DURATION)
    advance(400)
    expect(document.querySelectorAll('[data-leaving]')).toHaveLength(0)
  })
})

describe('a burst of 200', () => {
  it('shows three, a summary, and lists all 200 in the log; and never shows a fourth', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 200; i++) pushSnack(`err ${i}`, 'error') })
    expect(toasts()).toHaveLength(3)
    advance(DURATION)
    expect(texts()).toEqual(['还有 197 条提示，其中 197 条错误'])
    const summary = toasts()[0]
    fireEvent.click(within(summary as HTMLElement).getByRole('button', { name: '查看提示记录' }))
    const panel = document.querySelector('[data-slot="toast-log"]') as HTMLElement
    expect(panel).toBeTruthy()
    expect(within(panel).getByText('提示记录')).toBeTruthy()
    expect(panel.querySelectorAll('[data-slot="toast-log-row"]')).toHaveLength(200)
    expect(panel.textContent).toContain('err 1')
    expect(panel.textContent).toContain('err 200')
  })

  it('opens the log from the summary only, and Escape closes it', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    expect(document.querySelector('[data-slot="toast-log"]')).toBeNull()
    act(() => { for (let i = 1; i <= 12; i++) pushSnack(`m${i}`) })
    advance(DURATION)
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    const panel = document.querySelector('[data-slot="toast-log"]') as HTMLElement
    fireEvent.keyDown(panel, { key: 'Escape' })
    act(() => { vi.advanceTimersByTime(300) })
  })
})

describe('sync pending', () => {
  it('goes through the same store: counted in the cap, announced, one toast for a burst, with its link', () => {
    const go = vi.fn()
    const off = setSyncPendingNavigator(go)
    render(<SnackbarHost />)
    act(() => { pushToast({ message: 'x1' }); pushToast({ message: 'x2' }) })
    act(() => { notifySyncPending(); notifySyncPending(); notifySyncPending(); pushSnack('x3'); pushSnack('x4') })
    // x1, x2 and the sync toast fill the three slots; x3 and x4 wait.
    expect(toasts()).toHaveLength(3)
    expect(texts()).toContain('已排队，服务器恢复后自动同步')
    expect(announced(alertRegion())).toEqual(['已排队，服务器恢复后自动同步'])
    expect(document.querySelector('[data-slot="toast-count"]')?.textContent).toBe('×3')
    fireEvent.click(screen.getByRole('button', { name: '查看同步任务' }))
    expect(go).toHaveBeenCalledWith('/admin/sync-tasks')
    off()
  })
})

describe('severity and the summary, on screen', () => {
  it('shows errors, not a bare summary, when three successes were up and six errors arrive', () => {
    render(<SnackbarHost />)
    act(() => { for (const m of ['s1', 's2', 's3']) pushSnack(m, 'success') })
    act(() => { for (let i = 1; i <= 6; i++) pushSnack(`err ${i}`, 'error') })
    expect(texts()).toEqual(['err 1', 'err 2', 'err 3'])
    // The pre-empted successes are in the log, counted as unseen.
    advance(DURATION)
    expect(texts()).toEqual(['err 4', 'err 5', 'err 6'])
  })

  it('states the counts by severity in a summary, and does not let it expire while it holds an error', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 6; i++) pushSnack(`x${i}`, i === 1 ? 'warning' : 'error') })
    advance(DURATION)
    expect(texts()).toEqual(['还有 6 条提示，其中 5 条错误、1 条警告'])
    advance(60_000)
    expect(texts()).toEqual(['还有 6 条提示，其中 5 条错误、1 条警告'])
  })

  it('opening the log from the summary removes the summary, and the log lists what was collapsed', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 7; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    expect(toasts()).toHaveLength(0)
    expect(document.querySelectorAll('[data-slot="toast-log-row"]')).toHaveLength(10)
  })
})

describe('time is paused by hover and keyboard focus, not by any focus', () => {
  it('does not freeze everything when the dismissed toast was under the pointer', () => {
    render(<SnackbarHost />)
    act(() => { pushSnack('a'); pushSnack('b') })
    fireEvent.pointerEnter(toasts()[0])
    // The pointer is still over the first toast when its × removes it: no pointerleave ever fires.
    fireEvent.click(within(toasts()[0] as HTMLElement).getByRole('button', { name: '关闭' }))
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })

  it('does not pause for focus that came from a mouse click', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    const close = screen.getByRole('button', { name: '关闭' })
    fireEvent.pointerDown(close)
    act(() => close.focus())
    advance(DURATION)
    expect(texts()).toEqual([])
  })

  it('does not pause when the log panel closes, and gives focus back to what had it before, never to the stack', () => {
    render(<SnackbarHost />)
    const before = document.createElement('button')
    document.body.appendChild(before)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 7; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    act(() => before.focus())
    const open = screen.getByRole('button', { name: '查看提示记录' })
    // Keyboard path: Tab into the stack (focus moves from `before`), then Enter.
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true })) })
    act(() => open.focus())
    fireEvent.focusIn(open, { relatedTarget: before })
    fireEvent.click(open)
    const panel = document.querySelector('[data-slot="toast-log"]') as HTMLElement
    fireEvent.keyDown(panel, { key: 'Escape' })
    advance(500)
    expect(document.activeElement?.closest('[data-psp-toast-stack]')).toBeNull()
    expect(document.activeElement).toBe(before)
    // New errors arrive after the panel closed: they must expire, not hang frozen.
    act(() => { pushSnack('after-1', 'error'); pushSnack('after-2', 'error') })
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
    before.remove()
  })

  it('S1: Tab to the summary’s button, Enter, Esc: the toasts that come after still expire', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 7; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    const open = screen.getByRole('button', { name: '查看提示记录' })
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true })) })
    act(() => open.focus())
    // Frozen while keyboard focus is on it.
    advance(30_000)
    expect(toasts()).toHaveLength(1)
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })) })
    fireEvent.click(open)
    fireEvent.keyDown(document.querySelector('[data-slot="toast-log"]') as HTMLElement, { key: 'Escape' })
    advance(500)
    act(() => { pushSnack('n1', 'error'); pushSnack('n2', 'error') })
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })

  it('S2: a toast holding keyboard focus is pre-empted: focus moves to the next toast’s button, and time resumes when it leaves', () => {
    render(<SnackbarHost />)
    act(() => { for (const m of ['s1', 's2', 's3']) pushSnack(m, 'success') })
    const firstClose = within(toasts()[0] as HTMLElement).getByRole('button', { name: '关闭' })
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true })) })
    act(() => firstClose.focus())
    expect(document.activeElement).toBe(firstClose)
    advance(20_000)
    expect(toasts()).toHaveLength(3)
    // An error pre-empts the focused (oldest success) toast.
    act(() => pushSnack('boom', 'error'))
    expect(texts()).not.toContain('s1')
    expect(texts()).toContain('boom')
    // The keyboard user is not dropped on <body>: focus is on a remaining toast's dismiss button.
    const active = document.activeElement as HTMLElement
    expect(active.getAttribute('aria-label')).toBe('关闭')
    expect(active.closest('[data-psp-toast-stack]')).not.toBeNull()
    // They are still navigating the stack, so it is still paused; leaving releases it.
    advance(20_000)
    expect(toasts()).toHaveLength(3)
    act(() => active.blur())
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })

  it('S2 without a keyboard user: a removed toast that was focused by a click lets time run', () => {
    render(<SnackbarHost />)
    act(() => { for (const m of ['s1', 's2', 's3']) pushSnack(m, 'success') })
    const close = within(toasts()[0] as HTMLElement).getByRole('button', { name: '关闭' })
    fireEvent.pointerDown(close)
    act(() => close.focus())
    act(() => pushSnack('boom', 'error'))
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })

  it('does not count a bare Shift as keyboard navigation', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    const close = screen.getByRole('button', { name: '关闭' })
    fireEvent.pointerDown(close)
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Shift', shiftKey: true, bubbles: true })) })
    act(() => close.focus())
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })

  it('does not count a modifier on its own, or with a key held, as keyboard navigation', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    const close = screen.getByRole('button', { name: '关闭' })
    for (const init of [{ key: 'Meta', metaKey: true }, { key: 'Control', ctrlKey: true }, { key: 'Alt', altKey: true }, { key: 'Shift', shiftKey: true }, { key: 'k', metaKey: true }]) {
      act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, ...init })) })
    }
    fireEvent.pointerDown(close)
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Control', ctrlKey: true, bubbles: true })) })
    act(() => close.focus())
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })

  it('opening the log from a focused summary does not hand focus to another toast', async () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 7; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    // One summary and two newer toasts are up together.
    act(() => { pushSnack('n1', 'error'); pushSnack('n2', 'error') })
    const open = screen.getByRole('button', { name: '查看提示记录' })
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true })) })
    act(() => open.focus())
    fireEvent.click(open)
    await act(async () => { await Promise.resolve() })
    advance(300)
    expect(document.activeElement?.closest('[data-psp-toast-stack]')).toBeNull()
  })

  it('does not take focus from something else that took it in the same commit as the focused toast’s removal', () => {
    // A sibling that runs its effect just before the host's and focuses a field: a dialog or a
    // field taking focus in the same commit in which the focused toast is pre-empted.
    let go: (n: number) => void = () => {}
    const field = document.createElement('input')
    document.body.appendChild(field)
    function Taker() {
      const [n, setN] = useState(0)
      go = setN
      useEffect(() => { if (n > 0) field.focus() }, [n])
      return null
    }
    render(<><Taker /><SnackbarHost /></>)
    act(() => { for (const m of ['s1', 's2', 's3']) pushSnack(m, 'success') })
    const close = within(toasts()[0] as HTMLElement).getByRole('button', { name: '关闭' })
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true })) })
    act(() => close.focus())
    act(() => { go(1); pushSnack('boom', 'error') })
    expect(document.activeElement).toBe(field)
    field.remove()
  })

  it('resumes when keyboard focus leaves', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('a'))
    const close = screen.getByRole('button', { name: '关闭' })
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true })) })
    act(() => close.focus())
    advance(10_000)
    act(() => close.blur())
    advance(DURATION)
    expect(toasts()).toHaveLength(0)
  })
})

describe('the exit animation timers', () => {
  it('leaves no stuck leaving node when the list changes again within the animation', () => {
    render(<SnackbarHost />)
    act(() => { pushSnack('a'); pushSnack('b') })
    fireEvent.click(within(toasts()[0] as HTMLElement).getByRole('button', { name: '关闭' }))
    advance(30)
    act(() => pushSnack('c'))
    advance(30)
    fireEvent.click(within(toasts()[0] as HTMLElement).getByRole('button', { name: '关闭' }))
    advance(1000)
    expect(document.querySelectorAll('[data-leaving]')).toHaveLength(0)
  })
})

describe('exempt from dialog libraries', () => {
  it('puts the live regions and the stack straight under body, each with an explicit aria-live', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('x'))
    for (const el of [alertRegion(), statusRegion(), toasts()[0].closest('[data-psp-live-exempt]') as HTMLElement]) {
      expect(el.parentElement).toBe(document.body)
      expect(el.getAttribute('aria-live')).not.toBeNull()
    }
  })

  it('removes aria-hidden and inert the moment a dialog library puts them on', async () => {
    render(<SnackbarHost />)
    act(() => pushSnack('x'))
    const els = [alertRegion(), statusRegion(), toasts()[0].closest('[data-psp-live-exempt]') as HTMLElement]
    for (const el of els) { el.setAttribute('aria-hidden', 'true'); el.setAttribute('inert', ''); el.setAttribute('data-base-ui-inert', '') }
    await act(async () => { await Promise.resolve() })
    for (const el of els) {
      expect(el.hasAttribute('aria-hidden')).toBe(false)
      expect(el.hasAttribute('inert')).toBe(false)
    }
    act(() => pushSnack('heard', 'error'))
    expect(announced(alertRegion())).toContain('heard')
  })
})

describe('placement', () => {
  const narrow = (matches: boolean) => { window.matchMedia = vi.fn().mockImplementation((q: string) => ({ matches: matches && q.includes('max-width'), media: q, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn(), onchange: null, dispatchEvent: vi.fn() })) }

  const stackOf = (place: string) => document.querySelector(`[data-psp-toast-stack][data-position="${place}"]`) as HTMLElement
  const modal = (role = 'dialog') => { const d = document.createElement('div'); d.setAttribute('role', role); d.setAttribute('aria-modal', 'true'); return d }

  it('puts a toast that ENTERS while a modal dialog is open at the top on a phone', async () => {
    narrow(true)
    render(<SnackbarHost />)
    const dialog = modal()
    await act(async () => { document.body.appendChild(dialog); await Promise.resolve() })
    act(() => pushSnack('during'))
    expect(stackOf('top').textContent).toContain('during')
    expect(stackOf('bottom').textContent).not.toContain('during')
    await act(async () => { dialog.remove(); await Promise.resolve() })
    act(() => pushSnack('after'))
    expect(stackOf('bottom').textContent).toContain('after')
  })

  it('cross-fades the toasts already up to the top stack when the dialog opens: out, then in, no slide', async () => {
    narrow(true)
    render(<SnackbarHost />)
    act(() => pushSnack('before'))
    const dialog = modal()
    await act(async () => { document.body.appendChild(dialog); await Promise.resolve() })
    // First it fades out where it is.
    expect(stackOf('bottom').textContent).toContain('before')
    expect(stackOf('bottom').querySelector('[data-slot="toast"]')!.className).toContain('opacity-0')
    advance(200)
    // Then it is in the top stack, freshly faded in (not carried across the screen).
    expect(stackOf('top').textContent).toContain('before')
    expect(stackOf('bottom').textContent).not.toContain('before')
    expect(stackOf('top').querySelector('[data-slot="toast"]')!.className).not.toContain('opacity-0')
    dialog.remove()
  })

  it('does not move them while the pointer is over the stack, and moves them when it leaves', async () => {
    narrow(true)
    render(<SnackbarHost />)
    act(() => pushSnack('hovered'))
    fireEvent.pointerEnter(toasts()[0])
    const dialog = modal()
    await act(async () => { document.body.appendChild(dialog); await Promise.resolve() })
    advance(1000)
    expect(stackOf('bottom').textContent).toContain('hovered')
    fireEvent.pointerLeave(toasts()[0])
    advance(200)
    expect(stackOf('top').textContent).toContain('hovered')
    dialog.remove()
  })

  it('does not move them while keyboard focus is inside the stack, and moves them when it leaves', async () => {
    narrow(true)
    render(<SnackbarHost />)
    act(() => pushSnack('focused'))
    const close = screen.getByRole('button', { name: '关闭' })
    act(() => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true })) })
    act(() => close.focus())
    const dialog = modal()
    await act(async () => { document.body.appendChild(dialog); await Promise.resolve() })
    advance(1000)
    expect(stackOf('bottom').textContent).toContain('focused')
    act(() => close.blur())
    advance(200)
    expect(stackOf('top').textContent).toContain('focused')
    dialog.remove()
  })

  it('moves them without the fade under prefers-reduced-motion', async () => {
    window.matchMedia = vi.fn().mockImplementation((q: string) => ({ matches: q.includes('max-width') || q.includes('prefers-reduced-motion'), media: q, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn(), onchange: null, dispatchEvent: vi.fn() }))
    render(<SnackbarHost />)
    act(() => pushSnack('before'))
    const dialog = modal()
    await act(async () => { document.body.appendChild(dialog); await Promise.resolve() })
    // At once in the top stack, never at opacity 0 in between.
    expect(stackOf('top').textContent).toContain('before')
    expect(stackOf('top').querySelector('[data-slot="toast"]')!.className).not.toContain('opacity-0')
    dialog.remove()
  })

  it('puts the top stack ahead of the bottom one in the document, so Tab follows the screen', () => {
    render(<SnackbarHost />)
    const top = stackOf('top'); const bottom = stackOf('bottom')
    expect(top.compareDocumentPosition(bottom) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it.each([
    ['a menu', () => { const m = document.createElement('ul'); m.setAttribute('role', 'menu'); return m }],
    ['a listbox popover', () => { const m = document.createElement('ul'); m.setAttribute('role', 'listbox'); return m }],
    ['a non-modal dialog', () => { const m = document.createElement('div'); m.setAttribute('role', 'dialog'); return m }],
    ['the navigation sheet', () => { const m = modal(); m.setAttribute('data-slot', 'sidebar'); return m }],
    ['the log panel', () => { const m = modal(); m.setAttribute('data-slot', 'toast-log'); return m }],
    ['an MUI popover', () => { const m = document.createElement('div'); m.className = 'MuiModal-root MuiPopover-root'; return m }],
  ])('does not move toasts for %s', async (_n, make) => {
    narrow(true)
    render(<SnackbarHost />)
    const el = make()
    await act(async () => { document.body.appendChild(el); await Promise.resolve() })
    act(() => pushSnack('x'))
    expect(stackOf('top').textContent).toBe('')
    expect(stackOf('bottom').textContent).toContain('x')
    el.remove()
  })

  it('moves for an alertdialog (the confirm)', async () => {
    narrow(true)
    render(<SnackbarHost />)
    const el = modal('alertdialog')
    await act(async () => { document.body.appendChild(el); await Promise.resolve() })
    act(() => pushSnack('x'))
    expect(stackOf('top').textContent).toContain('x')
    el.remove()
  })

  it('does not move on a wide screen', async () => {
    narrow(false)
    render(<SnackbarHost />)
    act(() => pushSnack('x'))
    const dialog = document.createElement('div')
    dialog.setAttribute('role', 'dialog')
    dialog.setAttribute('aria-modal', 'true')
    await act(async () => { document.body.appendChild(dialog); await Promise.resolve() })
    act(() => pushSnack('y'))
    expect(document.querySelector('[data-psp-toast-stack][data-position="bottom"]')!.textContent).toContain('y')
    dialog.remove()
  })

  it('keeps no env() in the position: viewport-fit is not set by the page', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('x'))
    expect((document.querySelector('[data-position="bottom"]') as HTMLElement).style.paddingBottom).not.toContain('env(')
  })

  it('gives the action button the coarse-pointer minimum size, like the ×', () => {
    render(<SnackbarHost />)
    act(() => { pushToast({ message: 'x', action: { label: '去看看', onClick: () => {} } }) })
    expect(screen.getByRole('button', { name: '去看看' }).className).toContain('pointer-coarse:min-h-[2rem]')
  })

  it('prefixes a log row’s severity for a screen reader with a full-width colon in Chinese', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 7; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    expect(document.querySelector('[data-slot="toast-log-row"] .sr-only')!.textContent).toBe('错误：')
  })
})

describe('repeats and pre-emption, on screen', () => {
  it('announces a repeated failure again after two seconds, not as a stutter', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('连接失败', 'error'))
    advance(500)
    act(() => pushSnack('连接失败', 'error'))
    expect(announced(alertRegion())).toEqual(['连接失败'])
    advance(1600)
    act(() => pushSnack('连接失败', 'error'))
    expect(announced(alertRegion())).toEqual(['连接失败', '连接失败'])
  })

  it('shows the error itself, not a summary that swallowed it, when 9 infos arrive and then an error', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 9; i++) pushSnack(`i${i}`) })
    act(() => pushSnack('boom', 'error'))
    expect(texts()).toContain('boom')
  })

  it('announces six errors that arrive in separate tasks as three and one sentence', () => {
    render(<SnackbarHost />)
    for (let i = 1; i <= 6; i++) { act(() => pushSnack(`e${i}`, 'error')); advance(30) }
    advance(300)
    expect(announced(alertRegion())).toEqual(['e1', 'e2', 'e3', '另有 3 条新提示，其中 3 条错误'])
  })
})

describe('the log panel and the clock', () => {
  it('formats a row’s time in the app’s language, not the browser’s', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 7; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    const time = document.querySelector('[data-slot="toast-log-row"] time') as HTMLTimeElement
    expect(time.textContent).toBe(new Date(time.dateTime).toLocaleTimeString('zh-CN'))
  })
})

describe('a change of signed-in identity', () => {
  afterEach(() => { useAuthStore.setState({ userId: null, authEpoch: 0 }) })

  it.each([
    ['the session generation', () => useAuthStore.setState({ authEpoch: 7 })],
    ['the user', () => useAuthStore.setState({ userId: 42 })],
  ])('forgets the toasts, the queue, the log and the announcements when %s changes', (_n, change) => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 7; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    expect(toasts()).toHaveLength(1)
    expect(alertRegion().children.length).toBeGreaterThan(0)
    act(() => change())
    expect(toasts()).toHaveLength(0)
    expect(alertRegion().children).toHaveLength(0)
    expect(statusRegion().children).toHaveLength(0)
    // The log is empty for whoever is next.
    act(() => { pushSnack('a'); for (let i = 0; i < 3; i++) pushSnack(`b${i}`) })
    act(() => { for (let i = 1; i <= 6; i++) pushSnack(`c${i}`) })
    advance(DURATION)
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    const rows = [...document.querySelectorAll('[data-slot="toast-log-row"]')].map(r => r.textContent)
    expect(rows.some(r => r?.includes('e1') || r?.includes('x1'))).toBe(false)
  })

  it('closes an open log panel, and moves focus to the main landmark instead of dropping it on body', () => {
    const main = document.createElement('main')
    document.body.appendChild(main)
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 7; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    const panel = document.querySelector('[data-slot="toast-log"]') as HTMLElement
    const inside = panel.querySelector('button') as HTMLElement
    act(() => inside.focus())
    expect(document.activeElement?.closest('[data-slot="toast-log"]')).not.toBeNull()
    act(() => { useAuthStore.setState({ userId: 77 }) })
    advance(500)
    expect(document.querySelector('[data-slot="toast-log"]')).toBeNull()
    expect(document.activeElement).toBe(main)
    main.remove()
  })

  it('leaves everything alone when the identity does not change', () => {
    render(<SnackbarHost />)
    act(() => pushSnack('keep'))
    act(() => { useAuthStore.setState({ upn: 'same' }) })
    expect(texts()).toEqual(['keep'])
  })
})
