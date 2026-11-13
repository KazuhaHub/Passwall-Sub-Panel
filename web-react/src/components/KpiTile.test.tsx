// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ThemeProvider } from '@mui/material'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { createAppTheme, DEFAULT_PRESET_HEX } from '@/theme'
import KpiTile, { KpiGrid } from './KpiTile'

const theme = createAppTheme({ mode: 'light', sourceColor: DEFAULT_PRESET_HEX, language: 'en-US' })
const wrap = (ui: React.ReactNode) => render(<ThemeProvider theme={theme}>{ui}</ThemeProvider>)

afterEach(cleanup)

describe('KpiTile', () => {
  it('is a static tile without onToggle: no button, no aria-pressed', () => {
    wrap(<KpiGrid><KpiTile label="Rules" value="12" caption="of 256" /></KpiGrid>)
    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.getByText('12')).toBeTruthy()
    expect(screen.getByText('of 256')).toBeTruthy()
  })

  it('is a toggle with aria-pressed when onToggle is given, and a second click asks to clear', () => {
    const onToggle = vi.fn()
    const { rerender } = wrap(<KpiTile label="Problems" value="1" pressed={false} onToggle={onToggle} />)
    const button = screen.getByRole('button', { name: /Problems/ })
    expect(button.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(button)
    expect(onToggle).toHaveBeenCalledTimes(1)

    rerender(<ThemeProvider theme={theme}><KpiTile label="Problems" value="1" pressed onToggle={onToggle} /></ThemeProvider>)
    expect(screen.getByRole('button', { name: /Problems/ }).getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: /Problems/ }))
    expect(onToggle).toHaveBeenCalledTimes(2)
  })
})
