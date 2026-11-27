// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { renderAccess } from '@/test/accessControlHarness'
import ReleaseNotes from './ReleaseNotes'
import LegalDocument from './LegalDocument'

// What react-markdown hands the list and table renderers that the first version of
// them dropped: a table's column alignment, an ordered list's start number, and the
// task-list marker. Both renderers share the same components, so both are held to it.

// The stylesheet is not loaded in jsdom: alignment and list style are the utility classes that carry them.
const align = (el: Element) => ['left', 'center', 'right'].find(a => el.classList.contains(`text-${a}`)) ?? null

const mounts = [
  ['ReleaseNotes', (md: string) => render(<ReleaseNotes>{md}</ReleaseNotes>).container],
  ['LegalDocument', (md: string) => renderAccess(<LegalDocument content={md} dataCollection={null} />).container],
] as const

describe.each(mounts)('%s', (_name, mount) => {
  it('keeps GFM column alignment on header and body cells', () => {
    const c = mount('| left | centre | right | plain |\n| :-- | :-: | --: | --- |\n| a | b | c | d |\n')
    const th = [...c.querySelectorAll('th')]
    const td = [...c.querySelectorAll('td')]
    expect(th.map(align)).toEqual(['left', 'center', 'right', 'left'])
    expect(td.map(align)).toEqual(['left', 'center', 'right', null])
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
    expect(ul.classList.contains('list-none')).toBe(true)
  })

  it('still bullets an ordinary list that sits next to a task list', () => {
    const c = mount('- [x] done\n\ntext\n\n- plain\n')
    const lists = [...c.querySelectorAll('ul')]
    expect(lists.map(l => l.classList.contains('list-none') ? 'none' : l.classList.contains('list-disc') ? 'disc' : '?')).toEqual(['none', 'disc'])
  })
})
