// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { NEW_SCOPE_MEMBERS, expandScope } from '../../scripts/psp-scope.mjs'

describe('expandScope', () => {
  it('expands :psp-new to a zero-specificity :where() and :psp-new-strong to :is()', () => {
    const out = expandScope('h1:psp-new{a:b}h2:psp-new-strong{c:d}')
    expect(out).toBe(`h1:where(${NEW_SCOPE_MEMBERS.join(', ')}){a:b}h2:is(${NEW_SCOPE_MEMBERS.join(', ')}){c:d}`)
    expect(out).not.toContain(':psp-new')
  })
  it('leaves other css alone', () => {
    expect(expandScope('.a{b:c}')).toBe('.a{b:c}')
  })
})

// The three-level nesting contract: new shell -> legacy page -> new component
// re-entering. An element is in new scope when its NEAREST marker is new.
describe('the new-scope selector', () => {
  document.body.innerHTML = `
    <div id="plain"><p id="plain-p"></p></div>
    <div id="shell" class="psp-ui">
      <header id="hdr" data-slot="header"><span id="hdr-span"></span></header>
      <div id="legacy" class="psp-legacy">
        <p id="legacy-p"><b id="legacy-b"></b></p>
        <div id="reenter" data-slot="button"><span id="reenter-span"></span></div>
      </div>
    </div>
    <div id="portal" data-slot="dialog-content"><b id="portal-b"></b></div>`
  const inScope = (id: string) =>
    document.getElementById(id)!.matches(`:is(${NEW_SCOPE_MEMBERS.join(', ')})`)

  it.each(['shell', 'hdr', 'hdr-span', 'reenter', 'reenter-span', 'portal', 'portal-b'])('%s is new', id => {
    expect(inScope(id)).toBe(true)
  })
  it.each(['plain', 'plain-p', 'legacy', 'legacy-p', 'legacy-b'])('%s is not new', id => {
    expect(inScope(id)).toBe(false)
  })
})
