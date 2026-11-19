// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { NEW_SCOPE_LEVELS, isNewChain, isSupportedChain, levelNeeded } from '../../scripts/psp-scope-chain.mjs'
import { assertNoPlaceholderImports, expandScope, newScopeMembers, newRootSelector } from '../../scripts/psp-scope.mjs'

describe('chain model', () => {
  it('is new iff the nearest marker is U', () => {
    expect(isNewChain('')).toBe(false)
    expect(isNewChain('U')).toBe(true)
    expect(isNewChain('UL')).toBe(false)
    expect(isNewChain('ULU')).toBe(true)
    expect(isNewChain('LU')).toBe(true)
  })
  it('counts the LU pairs a new element needs', () => {
    expect(levelNeeded('U')).toBe(0)
    expect(levelNeeded('UU')).toBe(0)
    expect(levelNeeded('LU')).toBe(1)
    expect(levelNeeded('ULU')).toBe(1)
    expect(levelNeeded('LULU')).toBe(2)
    expect(levelNeeded('ULULU')).toBe(2)
    expect(levelNeeded('LLUULLUU')).toBe(2)
  })
  it('supports NEW_SCOPE_LEVELS levels', () => {
    expect(NEW_SCOPE_LEVELS).toBe(4)
    expect(isSupportedChain('ULULULU')).toBe(true) // 3 pairs: level 3
    expect(isSupportedChain('ULULULULU')).toBe(false) // 4 pairs
    expect(isSupportedChain('ULULULUL')).toBe(true) // legacy: never misclassified
  })
})

describe('expandScope', () => {
  const list = newScopeMembers().join(', ')
  it('expands :psp-new to a zero-specificity :where() and :psp-new-strong to :is()', () => {
    const out = expandScope('h1:psp-new{a:b}h2:psp-new-strong{c:d}')
    expect(out).toBe(`h1:where(${list}){a:b}h2:is(${list}){c:d}`)
    expect(out).not.toContain(':psp-')
  })
  it('expands :psp-new-root to an entry point: new scope whose parent is not new', () => {
    expect(expandScope('a:psp-new-root{x:y}')).toBe(`a:where(${newRootSelector()}){x:y}`)
    expect(newRootSelector()).toContain(' > *)')
  })
  it('leaves other css alone, including the .psp-legacy class', () => {
    expect(expandScope('.a{b:c}.psp-legacy{d:e}')).toBe('.a{b:c}.psp-legacy{d:e}')
  })
  it('does not expand or reject placeholder names inside comments', () => {
    expect(expandScope('/* :psp-new and :psp-bogus */ a{b:c}')).toBe('/* :psp-new and :psp-bogus */ a{b:c}')
  })
  it('throws on an unknown :psp-* token instead of emitting an invalid selector', () => {
    expect(() => expandScope('a:psp-new-roots{x:y}')).toThrow(/psp-new-roots/)
    expect(() => expandScope('a:psp-nwe{x:y}')).toThrow(/psp-nwe/)
  })
  it('generates one pair of members per level', () => {
    expect(newScopeMembers(1)).toHaveLength(2)
    expect(newScopeMembers(4)).toHaveLength(8)
  })
})

describe('assertNoPlaceholderImports', () => {
  it('rejects a placeholder inside an @import-ed file', () => {
    const files: Record<string, string> = { '/x/a.css': 'a:psp-new{x:y}', '/x/b.css': '.b{c:d}' }
    const read = (p: string) => files[p]
    expect(() => assertNoPlaceholderImports('@import "./a.css";', '/x', read)).toThrow(/a\.css/)
    expect(() => assertNoPlaceholderImports('@import "./b.css" layer(x);', '/x', read)).not.toThrow()
  })
  it('ignores package imports it cannot resolve relatively', () => {
    expect(() => assertNoPlaceholderImports('@import "tailwindcss/theme.css" layer(theme);', '/x', () => '')).not.toThrow()
  })
})

// Exhaustive over every marker chain up to length 6 with the real generated
// selector. jsdom's selector engine is not Chrome's, so scripts/check-scope.mjs
// repeats this in headless Chrome up to length 8 (and runs from smoke:dist).
describe('the generated selector against the nearest-marker rule', () => {
  const kinds = ['c', 'd', 'L', 'p'] as const // .psp-ui, [data-slot], .psp-legacy, plain
  const sel = `:is(${newScopeMembers().join(', ')})`
  const rootSel = newRootSelector()
  const rootMismatches: string[] = []
  let checked = 0
  const mismatches: string[] = []
  const root = document.createElement('div')
  document.body.appendChild(root)

  function walk(parent: Element, chain: string, depth: number) {
    for (const k of kinds) {
      const el = document.createElement('div')
      if (k === 'c') el.className = 'psp-ui'
      if (k === 'd') el.setAttribute('data-slot', 'x')
      if (k === 'L') el.className = 'psp-legacy'
      parent.appendChild(el)
      const next = k === 'c' || k === 'd' ? chain + 'U' : k === 'L' ? chain + 'L' : chain
      if (isSupportedChain(next)) {
        checked++
        if (el.matches(sel) !== isNewChain(next)) mismatches.push(`${chain}|${k}`)
        // an entry point is a new element whose parent is not new
        if (isSupportedChain(chain) && el.matches(rootSel) !== (isNewChain(next) && !isNewChain(chain))) rootMismatches.push(`${chain}|${k}`)
      }
      if (depth < 6) walk(el, next, depth + 1)
    }
  }
  walk(root, '', 1)

  it('agrees on every chain of length <= 6', () => {
    expect(checked).toBeGreaterThan(4000)
    expect(mismatches.slice(0, 5)).toEqual([])
    expect(rootMismatches.slice(0, 5)).toEqual([])
  })
})
