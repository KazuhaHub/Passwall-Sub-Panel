/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import FieldHint from './FieldHint'
afterEach(cleanup)
it('opens a named detail dialog from a form without submitting and dismisses with Escape', async () => {
  const submit = vi.fn(event => event.preventDefault())
  render(<ThemeProvider theme={createAppTheme({ mode: 'dark', sourceColor: '#6750a4', language: 'en-US' })}><form onSubmit={submit}><FieldHint tone="amber" summary="Also applies to allowlist groups" detail="Allow rules run before allowlists." /></form></ThemeProvider>)
  fireEvent.click(screen.getByRole('button', { name: 'Also applies to allowlist groups' }))
  const dialog = await screen.findByRole('dialog', { name: 'Also applies to allowlist groups' })
  expect(within(dialog).getByText('Allow rules run before allowlists.')).toBeTruthy()
  expect(submit).not.toHaveBeenCalled()
  fireEvent.keyDown(dialog, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})
