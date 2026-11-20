import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8')

// THE DOCUMENT IS THE SCROLLER UNDER A STICKY TOP BAR, so a scrollIntoView target or a sticky
// heading that sits at the very top is hidden behind the bar. One token carries the bar's height;
// the bar, the stylesheet's scroll-padding and the access records' sticky headings all read it.
describe('the shell bar height token', () => {
  it('is defined once, used by both bars, and cleared by every in-page scroll target', () => {
    const css = read('../../styles/ui.css')
    expect(css).toMatch(/--shell-bar-h:\s*3\.5rem/)
    expect(css).toMatch(/scroll-padding-top:\s*calc\(var\(--shell-bar-h\)/)
    expect(read('./AppShell.tsx')).toContain('h-(--shell-bar-h)')
    expect(read('./UserShell.tsx')).toContain('h-(--shell-bar-h)')
    expect(read('../../views/admin/accessControl/records/RecordsTab.tsx')).toContain('var(--shell-bar-h')
  })
})
