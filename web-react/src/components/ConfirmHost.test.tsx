/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import ConfirmHost, { confirm } from './ConfirmHost'

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
afterEach(cleanup)

it.each([false, true])('keeps confirmation actions touch-accessible and resolves the answer (destructive=%s)', async destructive => {
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><ConfirmHost /></ThemeProvider>)
  let answer!: Promise<boolean>
  act(() => { answer = confirm({ title: 'Discard unsaved changes?', message: 'The draft will be discarded.', confirmText: 'Discard', destructive }) })
  await screen.findByRole('dialog', { name: 'Discard unsaved changes?' })
  for (const name of ['actions.cancel', 'Discard']) {
    const button = screen.getByRole('button', { name })
    expect(parseFloat(getComputedStyle(button).minHeight)).toBeGreaterThanOrEqual(44)
  }
  fireEvent.click(screen.getByRole('button', { name: destructive ? 'Discard' : 'actions.cancel' }))
  expect(await answer).toBe(destructive)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})
