/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { useState } from 'react'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import ConfirmHost, { confirm } from './ConfirmHost'

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
afterEach(cleanup)

it.each(['cancel', 'escape'] as const)('returns to a temporarily disabled save action after %s', async action => {
  const resolved = vi.fn()
  function SaveAction() {
    const [busy, setBusy] = useState(false)
    return <button disabled={busy} onClick={async () => {
      setBusy(true)
      try { resolved(await confirm({ title: 'Enable first policy?', message: 'Consequence.', confirmText: 'Enable' })) }
      finally { setBusy(false) }
    }}>Save</button>
  }
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#0061a4', language: 'en-US' })}><SaveAction /><ConfirmHost /></ThemeProvider>)
  const trigger = screen.getByRole('button', { name: 'Save' })
  trigger.focus(); fireEvent.click(trigger)
  const dialog = await screen.findByRole('dialog')
  expect(trigger.hasAttribute('disabled')).toBe(true)
  if (action === 'escape') fireEvent.keyDown(dialog, { key: 'Escape' })
  else fireEvent.click(screen.getByRole('button', { name: 'actions.cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(resolved).toHaveBeenCalledExactlyOnceWith(false)
  expect(trigger.hasAttribute('disabled')).toBe(false)
  await waitFor(() => expect(document.activeElement).toBe(trigger))
})

it.each(['cancel', 'confirm', 'escape'] as const)('returns focus to the persistent action button after %s when the menu item unmounts', async action => {
  const { rerender } = render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#0061a4', language: 'en-US' })}><button key="trigger">Row actions</button><button key="menu">Transient menu item</button><ConfirmHost key="host" /></ThemeProvider>)
  const trigger = screen.getByRole('button', { name: 'Row actions' })
  screen.getByRole('button', { name: 'Transient menu item' }).focus()
  let answer!: Promise<boolean>
  act(() => { answer = confirm({ title: 'Delete?', message: 'Consequence.', confirmText: 'Delete' }, trigger) })
  rerender(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#0061a4', language: 'en-US' })}><button key="trigger">Row actions</button><ConfirmHost key="host" /></ThemeProvider>)
  const dialog = await screen.findByRole('dialog')
  if (action === 'escape') fireEvent.keyDown(dialog, { key: 'Escape' })
  else fireEvent.click(screen.getByRole('button', { name: action === 'confirm' ? 'Delete' : 'actions.cancel' }))
  expect(await answer).toBe(action === 'confirm')
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(trigger))
})

function rgb(hex: string): string {
  const value = parseInt(hex.slice(1), 16)
  return `rgb(${value >> 16 & 255}, ${value >> 8 & 255}, ${value & 255})`
}

it.each(['light', 'dark'] as const)('uses actual danger and ordinary action colors in %s mode', async mode => {
  const theme = createAppTheme({ mode, sourceColor: '#0061a4', language: 'en-US' })
  render(<ThemeProvider theme={theme}><ConfirmHost /></ThemeProvider>)
  for (const destructive of [true, false]) {
    let answer!: Promise<boolean>
    act(() => { answer = confirm({ title: 'Action?', message: 'Consequence.', confirmText: 'Apply', destructive }) })
    const button = await screen.findByRole('button', { name: 'Apply' })
    expect(getComputedStyle(button).backgroundColor).toBe(rgb(destructive ? theme.palette.md.error : theme.palette.md.primary))
    expect(getComputedStyle(button).color).toBe(rgb(destructive ? theme.palette.md.onError : theme.palette.md.onPrimary))
    fireEvent.click(screen.getByRole('button', { name: 'actions.cancel' }))
    expect(await answer).toBe(false)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  }
})

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
