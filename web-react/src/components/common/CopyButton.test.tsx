// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const copy = vi.hoisted(() => vi.fn())
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))
vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

import { CopyButton, CopyText } from './CopyButton'

beforeEach(() => { vi.useFakeTimers(); copy.mockReset(); copy.mockResolvedValue(true) })
afterEach(() => { cleanup(); vi.useRealTimers() })

async function click(el: HTMLElement) {
  await act(async () => { fireEvent.click(el) })
}

describe('CopyButton', () => {
  it('copies the value silently and confirms in place for a moment', async () => {
    render(<CopyButton value="https://x/sub/abc" name="订阅链接" />)
    const button = screen.getByRole('button', { name: '复制订阅链接' })
    await click(button)
    // The toast is suppressed: the control confirms in place instead.
    expect(copy).toHaveBeenCalledWith('https://x/sub/abc', { silent: true })
    expect(screen.getByRole('button', { name: '已复制' })).toBeTruthy()
    expect(screen.getByRole('status').textContent).toBe('已复制')
    await act(async () => { vi.advanceTimersByTime(1600) })
    expect(screen.getByRole('button', { name: '复制订阅链接' })).toBeTruthy()
    expect(screen.getByRole('status').textContent).toBe('')
  })

  it('stays unconfirmed when the copy did not happen', async () => {
    copy.mockResolvedValue(false)
    render(<CopyButton value="v" />)
    await click(screen.getByRole('button', { name: '复制' }))
    expect(screen.getByRole('button', { name: '复制' })).toBeTruthy()
  })
})

describe('CopyText', () => {
  it('shows the display text but copies the real value', async () => {
    render(<CopyText value="1234567890abcdef" display="1234…cdef" name="用户 ID" />)
    const button = screen.getByRole('button', { name: '复制用户 ID: 1234…cdef' })
    expect(button.textContent).toBe('1234…cdef')
    await click(button)
    expect(copy).toHaveBeenCalledWith('1234567890abcdef', { silent: true })
    expect(screen.getByRole('status').textContent).toBe('已复制')
  })
})
