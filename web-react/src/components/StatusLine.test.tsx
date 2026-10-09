/** @vitest-environment jsdom */
import { cleanup, render, screen } from '@testing-library/react'
import { ThemeProvider } from '@mui/material'
import { afterEach, expect, it } from 'vitest'
import { createAppTheme } from '@/theme'
import StatusLine from './StatusLine'
afterEach(cleanup)
const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
it('retains the existing whole-line live region for callers without a separate announcement', () => {
  render(<ThemeProvider theme={theme}><StatusLine tone="ok" title="Applied" meta="Last read" /></ThemeProvider>)
  expect(screen.getByRole('status').textContent).toContain('Last read')
})
it('announces only the verdict and keeps the hidden region narrow enough for a mobile viewport', () => {
  render(<ThemeProvider theme={theme}><StatusLine tone="measuring" title="Saved" announcement="Awaiting publication" detail="10 seconds" /></ThemeProvider>)
  const live = screen.getByRole('status')
  expect(live.textContent).toBe('Awaiting publication')
  expect(window.getComputedStyle(live).width).toBe('1px')
  expect(screen.getByText('Saved').getAttribute('aria-hidden')).toBe('true')
})
