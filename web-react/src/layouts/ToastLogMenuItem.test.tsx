// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { Menu } from '@mui/material'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k } }))

import SnackbarHost, { pushSnack } from '@/components/SnackbarHost'
import * as toasts from '@/components/feedback/toasts'
import { createAppTheme } from '@/theme'
import ToastLogMenuItem from './ToastLogMenuItem'

beforeEach(() => { vi.useFakeTimers() })
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
const mount = (onClose: () => void) => render(
  <ThemeProvider theme={theme}>
    <SnackbarHost />
    <Menu open anchorEl={document.body}><ToastLogMenuItem onClose={onClose} /></Menu>
  </ThemeProvider>,
)

describe('ToastLogMenuItem', () => {
  it('closes the menu, then opens the log, when chosen', () => {
    const open = vi.spyOn(toasts, 'openToastLog').mockImplementation(() => {})
    const onClose = vi.fn()
    mount(onClose)
    fireEvent.click(screen.getByRole('menuitem'))
    expect(onClose).toHaveBeenCalledTimes(1)
    // After the menu has handed focus back, not in the same tick.
    expect(open).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(10) })
    expect(open).toHaveBeenCalledTimes(1)
  })

  it('shows the count it claims, and the total never goes down when an error joins', () => {
    mount(() => {})
    const count = () => document.querySelector('[data-slot="toast-log-menu-count"]')?.textContent ?? null
    expect(count()).toBeNull()
    act(() => { for (let i = 1; i <= 3; i++) pushSnack(`i${i}`) })
    act(() => { for (let i = 1; i <= 8; i++) pushSnack(`x${i}`) })
    const before = Number(count())
    expect(before).toBeGreaterThan(0)
    expect(screen.getByRole('menuitem').getAttribute('aria-label')).toBe(`提示记录，${before} 条未查看`)
    // Errors arrive and are collapsed: the total rises, it is not replaced by the error count.
    act(() => { for (let i = 1; i <= 4; i++) pushSnack(`e${i}`, 'error') })
    const after = Number(count())
    expect(after).toBeGreaterThanOrEqual(before)
    expect(screen.getByRole('menuitem').getAttribute('aria-label')).toMatch(/其中.*错误/)
  })
})
