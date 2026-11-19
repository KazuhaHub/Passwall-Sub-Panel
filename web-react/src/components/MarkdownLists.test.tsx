// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import { createAppTheme } from '@/theme'
import { renderAccess } from '@/test/accessControlHarness'
import ReleaseNotes from './ReleaseNotes'
import LegalDocument from './LegalDocument'

// Tailwind's reset sets `ol, ul { list-style: none }`. In a terms document the
// numbering IS content ("clause 3"), so the two markdown renderers must put the
// markers back themselves.
const md = '- bullet one\n- bullet two\n\n1. first\n2. second\n\n> quoted\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\n```\ncode block\n```\n'

const theme = createAppTheme({ mode: 'light', sourceColor: '#0061A4', language: 'en-US' })
const style = (el: Element, prop: string) => getComputedStyle(el).getPropertyValue(prop)

describe.each([
  ['ReleaseNotes', () => render(<ThemeProvider theme={theme}><ReleaseNotes>{md}</ReleaseNotes></ThemeProvider>).container],
  ['LegalDocument', () => renderAccess(<LegalDocument content={md} dataCollection={null} />).container],
])('%s lists and blocks', (_name, mount) => {
  it('draws bullets on ul and numbers on ol', () => {
    const c = mount()
    expect(style(c.querySelector('ul')!, 'list-style-type')).toBe('disc')
    expect(style(c.querySelector('ol')!, 'list-style-type')).toBe('decimal')
  })
  it('keeps indentation on lists', () => {
    expect(style(mount().querySelector('ol')!, 'padding-left')).not.toMatch(/^0(px)?$/)
  })
  it('gives block quotes, tables and code blocks their own structure', () => {
    const c = mount()
    expect(style(c.querySelector('blockquote')!, 'border-left-style')).toBe('solid')
    expect(style(c.querySelector('td')!, 'border-top-style')).toBe('solid')
    expect(style(c.querySelector('pre')!, 'white-space')).toBe('pre')
  })
})
