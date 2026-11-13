// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ThemeProvider } from '@mui/material'
import { afterEach, expect, it } from 'vitest'

import { createAppTheme, DEFAULT_PRESET_HEX } from '@/theme'
import FieldHint from './FieldHint'

afterEach(cleanup)
const theme = createAppTheme({ mode: 'light', sourceColor: DEFAULT_PRESET_HEX, language: 'en-US' })

it('amber: shows an icon beside the summary, keeps the words in onSurface, and opens the detail on click', () => {
  render(<ThemeProvider theme={theme}><FieldHint tone="amber" summary="Will exceed the quota" detail="Longer reasoning" /></ThemeProvider>)
  const link = screen.getByRole('button', { name: /Will exceed the quota/ })
  expect(link.querySelector('[data-testid="WarningAmberIcon"]')).not.toBeNull()
  expect(getComputedStyle(link).color).not.toBe('')
  expect(screen.queryByText('Longer reasoning')).toBeNull()
  fireEvent.click(link)
  expect(screen.getByText('Longer reasoning')).toBeTruthy()
})

it('the old warning tone draws no warning icon', () => {
  render(<ThemeProvider theme={theme}><FieldHint summary="s" detail="d" /></ThemeProvider>)
  expect(screen.getByRole('button').querySelector('[data-testid="WarningAmberIcon"]')).toBeNull()
})
