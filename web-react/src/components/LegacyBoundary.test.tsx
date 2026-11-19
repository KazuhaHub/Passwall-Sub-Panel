// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import { createAppTheme } from '@/theme'
import LegacyBoundary from './LegacyBoundary'

function boundary(mode: 'light' | 'dark', density: 'comfortable' | 'compact' = 'comfortable') {
  const theme = createAppTheme({ mode, sourceColor: '#0061A4', language: 'en-US', density })
  const { container } = render(
    <ThemeProvider theme={theme}><LegacyBoundary><i data-testid="kid" /></LegacyBoundary></ThemeProvider>,
  )
  return { el: container.firstElementChild as HTMLElement, theme }
}

describe('LegacyBoundary', () => {
  it('renders the .psp-legacy marker around its children', () => {
    const { el } = boundary('light')
    expect(el.classList.contains('psp-legacy')).toBe(true)
    expect(el.querySelector('[data-testid=kid]')).not.toBeNull()
  })

  it('re-establishes what CssBaseline puts on body, from the live theme', () => {
    const { el, theme } = boundary('light')
    const b1 = theme.typography.body1
    // jsdom re-serialises the list with spaces after the commas.
    expect(el.style.fontFamily.replace(/\s/g, '')).toBe(String(b1.fontFamily).replace(/\s/g, ''))
    expect(el.style.fontSize).toBe(String(b1.fontSize))
    expect(el.style.lineHeight).toBe(String(b1.lineHeight))
    expect(el.style.fontWeight).toBe(String(b1.fontWeight))
    expect(el.style.color).not.toBe('')
  })

  it('follows the mode', () => {
    const light = boundary('light').el.style.color
    const dark = boundary('dark').el.style.color
    expect(light).not.toBe(dark)
  })
})
