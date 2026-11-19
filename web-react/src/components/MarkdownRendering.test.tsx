// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import { createAppTheme } from '@/theme'
import { renderAccess } from '@/test/accessControlHarness'
import ReleaseNotes from './ReleaseNotes'
import LegalDocument from './LegalDocument'

// What react-markdown hands the list and table renderers that the first version of
// them dropped: a table's column alignment, an ordered list's start number, and the
// task-list marker. Both renderers share the same components, so both are held to it.

const theme = createAppTheme({ mode: 'light', sourceColor: '#0061A4', language: 'en-US' })
const style = (el: Element, prop: string) => getComputedStyle(el).getPropertyValue(prop)

const mounts = [
  ['ReleaseNotes', (md: string) => render(<ThemeProvider theme={theme}><ReleaseNotes>{md}</ReleaseNotes></ThemeProvider>).container],
  ['LegalDocument', (md: string) => renderAccess(<LegalDocument content={md} dataCollection={null} />).container],
] as const

describe.each(mounts)('%s', (_name, mount) => {
  it('keeps GFM column alignment on header and body cells', () => {
    const c = mount('| left | centre | right | plain |\n| :-- | :-: | --: | --- |\n| a | b | c | d |\n')
    const th = [...c.querySelectorAll('th')]
    const td = [...c.querySelectorAll('td')]
    expect(th.map(el => style(el, 'text-align'))).toEqual(['left', 'center', 'right', 'left'])
    expect(td.map(el => style(el, 'text-align'))).toEqual(['left', 'center', 'right', 'start'])
  })

  it('numbers an ordered list from its start', () => {
    const c = mount('3. third\n4. fourth\n')
    expect(c.querySelector('ol')!.getAttribute('start')).toBe('3')
  })

  it('leaves an ordered list that starts at 1 without a start attribute', () => {
    expect(mount('1. one\n2. two\n').querySelector('ol')!.getAttribute('start')).toBeNull()
  })

  it('draws a task list with its checkboxes and no bullet beside them', () => {
    const c = mount('- [x] done\n- [ ] todo\n')
    const ul = c.querySelector('ul')!
    expect(ul.querySelectorAll('input[type="checkbox"]')).toHaveLength(2)
    expect((ul.querySelector('input') as HTMLInputElement).checked).toBe(true)
    expect(style(ul, 'list-style-type')).toBe('none')
  })

  it('still bullets an ordinary list that sits next to a task list', () => {
    const c = mount('- [x] done\n\ntext\n\n- plain\n')
    const lists = [...c.querySelectorAll('ul')]
    expect(lists.map(l => style(l, 'list-style-type'))).toEqual(['none', 'disc'])
  })
})
