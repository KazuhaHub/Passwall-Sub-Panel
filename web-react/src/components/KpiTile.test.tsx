/** @vitest-environment jsdom */
import { useState } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { createAppTheme } from '@/theme'
import KpiTile from './KpiTile'

afterEach(cleanup)
const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

it('exposes a pressed toggle and clears it on a second click', () => {
  function Harness() {
    const [pressed, setPressed] = useState(false)
    return <KpiTile label="Blocked" value={12} pressed={pressed} onToggle={() => setPressed(v => !v)} />
  }
  render(<ThemeProvider theme={theme}><Harness /></ThemeProvider>)
  const tile = screen.getByRole('button', { name: /Blocked/ })
  expect(tile.getAttribute('aria-pressed')).toBe('false')
  fireEvent.click(tile)
  expect(tile.getAttribute('aria-pressed')).toBe('true')
  fireEvent.click(tile)
  expect(tile.getAttribute('aria-pressed')).toBe('false')
})

it('keeps a static statistic out of the keyboard button sequence', () => {
  render(<ThemeProvider theme={theme}><KpiTile label="Users" value={5} caption="today" /></ThemeProvider>)
  expect(screen.queryByRole('button')).toBeNull()
  expect(screen.getByText('today')).toBeTruthy()
})
