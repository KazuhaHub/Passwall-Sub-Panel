// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())
vi.mock('@/i18n', async () => {
  const { translate } = await import('@/test/i18nMock')
  return { default: { t: (k: string, o?: Record<string, unknown>) => translate('common', k, o) } }
})

import ToastHost from './ToastHost'
import { notifySyncPending, setSyncPendingNavigator, SYNC_TASKS_ROUTE } from './syncPendingToast'

// The cap, the announcement and the counter are tested with the host (ToastHost.test.tsx);
// this is what the sync-pending toast itself says and links to.
beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { cleanup(); vi.useRealTimers() })

const MESSAGE = '已排队，服务器恢复后自动同步'

describe('notifySyncPending', () => {
  it('says the change is queued and will sync by itself', () => {
    render(<ToastHost />)
    act(() => notifySyncPending())
    expect(screen.getAllByText(MESSAGE).length).toBeGreaterThan(0)
  })

  it('has no link when no shell can open the sync tasks page', () => {
    render(<ToastHost />)
    act(() => notifySyncPending())
    expect(screen.queryByRole('button', { name: '查看同步任务' })).toBeNull()
  })

  it('links to the sync tasks page through the registered navigator', () => {
    const go = vi.fn()
    const off = setSyncPendingNavigator(go)
    render(<ToastHost />)
    act(() => notifySyncPending())
    fireEvent.click(screen.getByRole('button', { name: '查看同步任务' }))
    expect(go).toHaveBeenCalledWith(SYNC_TASKS_ROUTE)
    expect(SYNC_TASKS_ROUTE).toBe('/admin/sync-tasks')
    off()
  })

  it('stops linking once the navigator is unregistered', () => {
    const off = setSyncPendingNavigator(vi.fn())
    off()
    render(<ToastHost />)
    act(() => notifySyncPending())
    expect(screen.queryByRole('button', { name: '查看同步任务' })).toBeNull()
  })

  it('is one toast, with a counter, however many times it is raised', () => {
    render(<ToastHost />)
    act(() => { notifySyncPending(); notifySyncPending(); notifySyncPending() })
    expect(document.querySelectorAll('[data-slot="toast"]')).toHaveLength(1)
    expect(document.querySelector('[data-slot="toast-count"]')?.textContent).toBe('×3')
  })
})
