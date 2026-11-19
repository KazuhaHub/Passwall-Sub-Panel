// @vitest-environment jsdom
import { useState } from 'react'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k } }))

import SnackbarHost, { pushSnack } from '../SnackbarHost'
import { ToastLogButton } from '../shell/ToastLogButton'
import { openToastLog, useToastLogSummary } from './toasts'
import { useToastBottomOffset } from './useToastBottomOffset'

const DURATION = 4000
beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { cleanup(); vi.useRealTimers(); document.documentElement.style.removeProperty('--toast-bottom-offset') })
const advance = (ms: number) => act(() => { vi.advanceTimersByTime(ms) })

// Three errors fill the slots, ten more collapse into a summary that holds errors.
function overflow() {
  act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
  act(() => { for (let i = 1; i <= 10; i++) pushSnack(`x${i}`, 'error') })
  advance(DURATION)
}

describe('the log stays reachable after its summary toast is gone', () => {
  it('has no badge until something goes unseen, and clears it once the log has been looked at', () => {
    render(<><SnackbarHost /><ToastLogButton /></>)
    expect(document.querySelector('[data-slot="toast-log-badge"]')).toBeNull()
    expect(screen.getByRole('button', { name: '提示记录' })).toBeTruthy()
    overflow()
    // Opening the log clears the badge at once, while the panel is still open.
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    expect(document.querySelector('[data-slot="toast-log"]')).toBeTruthy()
    expect(document.querySelector('[data-slot="toast-log-badge"]')).toBeNull()
  })

  it('counts unseen messages, errors first, and names them for a screen reader', () => {
    render(<><SnackbarHost /><ToastLogButton /></>)
    overflow()
    const badge = document.querySelector('[data-slot="toast-log-badge"]') as HTMLElement
    expect(badge.textContent).toBe('10')
    expect(badge.hasAttribute('data-errors')).toBe(true)
    expect(screen.getByRole('button', { name: '提示记录，10 条未查看，其中 10 条错误' })).toBeTruthy()
  })

  it('opens the panel without the summary toast, and closing it clears the badge', () => {
    render(<><SnackbarHost /><ToastLogButton /></>)
    overflow()
    fireEvent.click(screen.getByRole('button', { name: /^提示记录/ }))
    expect(document.querySelector('[data-slot="toast-log-badge"]')).toBeNull()
    const panel = document.querySelector('[data-slot="toast-log"]') as HTMLElement
    expect(panel).toBeTruthy()
    expect(panel.querySelectorAll('[data-slot="toast-log-row"]').length).toBe(13)
    fireEvent.keyDown(panel, { key: 'Escape' })
    advance(400)
    expect(document.querySelector('[data-slot="toast-log-badge"]')).toBeNull()
    expect(screen.getByRole('button', { name: '提示记录' })).toBeTruthy()
  })

  it('openToastLog() does nothing without a host, and useToastLogSummary starts at zero', () => {
    expect(() => openToastLog()).not.toThrow()
    function Probe() { const s = useToastLogSummary(); return <i data-testid="p">{s.unread}/{s.unreadErrors}</i> }
    render(<Probe />)
    expect(screen.getByTestId('p').textContent).toBe('0/0')
  })

  it('shows what the log bound left out, as a line in the panel', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 400; i++) pushSnack(`x${i}`, 'error') })
    advance(DURATION)
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    expect(document.querySelector('[data-slot="toast-log-omitted"]')!.textContent).toMatch(/^更早的 \d+ 条提示已省略$/)
  })
})

describe('10,000 pushes', () => {
  it('leave a bounded log, one summary, and a bounded number of announcement nodes', () => {
    render(<SnackbarHost />)
    act(() => { for (let i = 1; i <= 10_000; i++) pushSnack(`n${i}`, i % 50 === 0 ? 'error' : 'info') })
    advance(300)
    expect(document.querySelectorAll('[data-slot="toast"]').length).toBeLessThanOrEqual(3)
    expect(document.querySelectorAll('[data-slot="toast-live-alert"] > div, [data-slot="toast-live-status"] > div').length).toBeLessThanOrEqual(8)
    advance(DURATION * 2)
    expect(document.querySelectorAll('[data-kind="summary"]').length).toBeLessThanOrEqual(1)
    fireEvent.click(screen.getByRole('button', { name: '查看提示记录' }))
    expect(document.querySelectorAll('[data-slot="toast-log-row"]').length).toBeLessThanOrEqual(250)
  })
})

describe('the badge number', () => {
  it('is the unread total and never goes down as messages arrive, errors showing as colour and in the name', () => {
    render(<><SnackbarHost /><ToastLogButton /></>)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`i${i}`) })
    act(() => { for (let i = 1; i <= 8; i++) pushSnack(`x${i}`) })
    const badge = () => document.querySelector('[data-slot="toast-log-badge"]') as HTMLElement
    const before = Number(badge().textContent)
    expect(badge().hasAttribute('data-errors')).toBe(false)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 2; i++) pushSnack(`f${i}`, 'error') })
    expect(Number(badge().textContent)).toBeGreaterThanOrEqual(before)
    expect(badge().hasAttribute('data-errors')).toBe(true)
  })
})

describe('the badge', () => {
  it('is capped at 99+ and shows the error count when there are errors', () => {
    render(<><SnackbarHost /><ToastLogButton /></>)
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`e${i}`, 'error') })
    act(() => { for (let i = 1; i <= 160; i++) pushSnack(`x${i}`, 'error') })
    expect(document.querySelector('[data-slot="toast-log-badge"]')!.textContent).toBe('99+')
  })
})

describe('useToastBottomOffset', () => {
  function Bar({ px }: { px: number }) { useToastBottomOffset(px); return <div /> }
  const value = () => document.documentElement.style.getPropertyValue('--toast-bottom-offset')

  it('sets the variable while mounted and removes it on unmount', () => {
    const { unmount } = render(<Bar px={64} />)
    expect(value()).toBe('64px')
    unmount()
    expect(value()).toBe('')
  })

  it('follows a changing number, and an element’s height', () => {
    const { rerender } = render(<Bar px={40} />)
    rerender(<Bar px={72} />)
    expect(value()).toBe('72px')
    function RefBar() {
      const [el, setEl] = useState<HTMLDivElement | null>(null)
      useToastBottomOffset({ current: el })
      return <div ref={setEl} />
    }
    render(<RefBar />)
  })

  it('keeps the largest of several bars, and removes the variable only when the last unmounts', () => {
    const a = render(<Bar px={40} />)
    const b = render(<Bar px={90} />)
    expect(value()).toBe('90px')
    b.unmount()
    expect(value()).toBe('40px')
    a.unmount()
    expect(value()).toBe('')
    const c = render(<Bar px={10} />); const d = render(<Bar px={20} />)
    c.unmount()
    expect(value()).toBe('20px')
    d.unmount()
  })

  it('keeps the largest when a bigger bar mounts first and a smaller one after', () => {
    const big = render(<Bar px={90} />)
    const small = render(<Bar px={40} />)
    expect(value()).toBe('90px')
    big.unmount()
    expect(value()).toBe('40px')
    small.unmount()
    expect(value()).toBe('')
  })

  it('moves the toast stack clear of the bar', () => {
    render(<><SnackbarHost /><Bar px={64} /></>)
    act(() => pushSnack('x'))
    expect((document.querySelector('[data-position="bottom"]') as HTMLElement).style.paddingBottom).toContain('var(--toast-bottom-offset')
  })
})
