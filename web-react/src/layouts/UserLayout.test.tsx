// @vitest-environment jsdom
import type { ReactElement } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import UserLayout from './UserLayout'

vi.mock('@/i18n', () => ({
  default: { t: (key: string) => key, language: 'en-US' },
  setLanguage: vi.fn(), currentLanguage: () => 'en-US',
  SUPPORTED_LANGUAGES: ['zh-CN', 'en-US'], isBuiltinLanguage: () => true, serverLanguageMeta: () => undefined,
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }),
  Trans: ({ children }: { children: ReactElement }) => children,
}))
vi.mock('@/components/ConsentDialog', () => ({ default: () => null }))
vi.mock('@/components/SiteFooter', () => ({ default: () => null }))
vi.mock('@/api/client', () => ({ client: { get: vi.fn(() => new Promise(() => {})), post: vi.fn(), put: vi.fn(), delete: vi.fn() } }))

afterEach(cleanup)

describe('UserLayout', () => {
  // Until the new shell replaces this layout, the message log is reachable from this app bar.
  it('has the message log button in its app bar', async () => {
    render(
      <MemoryRouter>
        <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>
          <UserLayout />
        </ThemeProvider>
      </MemoryRouter>,
    )
    const log = await screen.findByRole('button', { name: 'ui.feedback.log_title' })
    expect(log.closest('header')).not.toBeNull()
    expect(log.className).toContain('hidden')
    expect(log.className).toContain('sm:inline-flex')
    // The bar-sized variant: 40px button, 24px icon, to match the MUI buttons beside it.
    expect(log.className).toContain('size-10')
    expect(log.className).toContain("[&_svg:not([class*='size-'])]:size-6")
  })

  it('carries the log in the account menu for widths where the button is hidden', async () => {
    render(
      <MemoryRouter>
        <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>
          <UserLayout />
        </ThemeProvider>
      </MemoryRouter>,
    )
    const avatar = await waitFor(() => document.querySelector('.MuiAvatar-root')!.closest('button') as HTMLElement)
    fireEvent.click(avatar)
    expect(await screen.findByRole('menuitem', { name: 'ui.feedback.log_title' })).toBeTruthy()
  })
})
