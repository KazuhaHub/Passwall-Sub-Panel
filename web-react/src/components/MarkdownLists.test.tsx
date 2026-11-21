// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { renderAccess } from '@/test/accessControlHarness'
import ReleaseNotes from './ReleaseNotes'
import LegalDocument from './LegalDocument'

// Tailwind's reset sets `ol, ul { list-style: none }`. In a terms document the
// numbering IS content ("clause 3"), so the two markdown renderers must put the
// markers back themselves. The stylesheet is not loaded in jsdom, so what is held
// here is the utility class that does it, not a computed style.
const md = '- bullet one\n- bullet two\n\n1. first\n2. second\n\n> quoted\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\n```\ncode block\n```\n'

const has = (el: Element, cls: string) => el.classList.contains(cls)

describe.each([
  ['ReleaseNotes', () => render(<ReleaseNotes>{md}</ReleaseNotes>).container],
  ['LegalDocument', () => renderAccess(<LegalDocument content={md} dataCollection={null} />).container],
])('%s lists and blocks', (_name, mount) => {
  it('draws bullets on ul and numbers on ol', () => {
    const c = mount()
    expect(has(c.querySelector('ul')!, 'list-disc')).toBe(true)
    expect(has(c.querySelector('ol')!, 'list-decimal')).toBe(true)
  })
  it('keeps indentation on lists', () => {
    expect(has(mount().querySelector('ol')!, 'pl-6')).toBe(true)
  })
  it('gives block quotes, tables and code blocks their own structure', () => {
    const c = mount()
    expect(has(c.querySelector('blockquote')!, 'border-l-[3px]')).toBe(true)
    expect(has(c.querySelector('td')!, 'border')).toBe(true)
    expect(has(c.querySelector('pre')!, 'whitespace-pre')).toBe(true)
  })
})
