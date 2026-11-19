// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import { createAppTheme } from '@/theme'
import LegacyBoundary, { chainOf } from './LegacyBoundary'

function boundary(mode: 'light' | 'dark', props: Record<string, unknown> = {}) {
  const theme = createAppTheme({ mode, sourceColor: '#0061A4', language: 'en-US' })
  const { container } = render(
    <ThemeProvider theme={theme}><LegacyBoundary {...props}><i data-testid="kid" /></LegacyBoundary></ThemeProvider>,
  )
  return { el: container.firstElementChild as HTMLElement, theme }
}

describe('LegacyBoundary', () => {
  it('renders the .psp-legacy marker around its children', () => {
    const { el } = boundary('light')
    expect(el.classList.contains('psp-legacy')).toBe(true)
    expect(el.querySelector('[data-testid=kid]')).not.toBeNull()
  })

  it('is layout-neutral by default: display: contents, no background', () => {
    const { el } = boundary('light')
    expect(el.style.display).toBe('contents')
    expect(el.style.backgroundColor).toBe('')
  })

  it('accepts className and style, and lets the caller override display', () => {
    const { el } = boundary('light', { className: 'page', style: { display: 'block', backgroundColor: 'red' } })
    expect(el.classList.contains('psp-legacy')).toBe(true)
    expect(el.classList.contains('page')).toBe(true)
    expect(el.style.display).toBe('block')
    expect(el.style.backgroundColor).toBe('red')
  })

  it('re-establishes what CssBaseline puts on body, from the live theme', () => {
    const { el, theme } = boundary('light')
    const b1 = theme.typography.body1
    expect(el.style.fontFamily.replace(/\s/g, '')).toBe(String(b1.fontFamily).replace(/\s/g, ''))
    expect(el.style.fontSize).toBe(String(b1.fontSize))
    expect(el.style.lineHeight).toBe(String(b1.lineHeight))
    expect(el.style.fontWeight).toBe(String(b1.fontWeight))
    expect(el.style.color).not.toBe('')
  })

  it('follows the mode', () => {
    expect(boundary('light').el.style.color).not.toBe(boundary('dark').el.style.color)
  })
})

describe('chainOf (dev depth check)', () => {
  it('reads the marker chain root-first from the DOM', () => {
    document.body.innerHTML = '<div class="psp-ui"><div class="psp-legacy"><section data-slot="x"><div class="psp-legacy" id="t"></div></section></div></div>'
    expect(chainOf(document.getElementById('t')!)).toBe('ULUL')
  })
})

describe('depth warning', () => {
  afterEach(() => { vi.restoreAllMocks(); document.body.innerHTML = '' })
  it('console.errors when a re-entry under the boundary would exceed the supported depth', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    const theme = createAppTheme({ mode: 'light', sourceColor: '#0061A4', language: 'en-US' })
    // chain U L U L U L U L -> a new element beneath needs 4 pairs
    const deep = (
      <div className="psp-ui"><div className="psp-legacy"><div data-slot="a"><div className="psp-legacy">
        <div data-slot="b"><div className="psp-legacy"><div data-slot="c">
          <ThemeProvider theme={theme}><LegacyBoundary>x</LegacyBoundary></ThemeProvider>
        </div></div></div></div></div></div></div>
    )
    render(deep)
    expect(err.mock.calls.some(c => String(c[0]).includes('psp-legacy'))).toBe(true)
  })
  it('stays quiet within the supported depth', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    const theme = createAppTheme({ mode: 'light', sourceColor: '#0061A4', language: 'en-US' })
    render(<div className="psp-ui"><ThemeProvider theme={theme}><LegacyBoundary>x</LegacyBoundary></ThemeProvider></div>)
    expect(err).not.toHaveBeenCalled()
  })
})
