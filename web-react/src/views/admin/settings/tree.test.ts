// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { SCOPE_KEYS } from '@/components/scope/scopeOverrides'
import { SECTIONS, SUB_KEYS, type SectionKey } from './model'
import { SEARCH_INDEX } from './searchIndex'
import { PLACES, TREE, firstSub, subOf, subsOf, type SubDef } from './tree'

// The tree is the one statement of where a setting lives. These walk it against the two other things that name
// settings (the search index, the group-override list) so that a setting can neither be missing from the page, nor
// be on it twice, nor be overridable in a place other than the one that holds its global value.

const holds = (d: SubDef, key: string) => d.keys.includes(key) || (d.prefix !== undefined && key.startsWith(d.prefix))

describe('the settings tree', () => {
  it('has a first sub-tab for every section, with ids that are unique within it', () => {
    for (const s of SECTIONS) {
      const ids = subsOf(s).map(d => d.id)
      expect(ids.length, s).toBeGreaterThan(0)
      expect(new Set(ids).size, s).toBe(ids.length)
      expect(firstSub(s)).toBe(ids[0])
    }
    expect(new Set(PLACES).size).toBe(PLACES.length)
  })

  it('places every setting the search knows in exactly one sub-tab of its section', () => {
    for (const e of SEARCH_INDEX) {
      const homes = subsOf(e.section).filter(d => holds(d, e.key))
      expect(homes.map(d => d.id), `${e.section}/${e.key}`).toHaveLength(1)
      expect(e.sub, `${e.section}/${e.key}`).toBe(homes[0].id)
      expect(subOf(e.section, e.key)).toBe(homes[0].id)
    }
  })

  it('lists no setting that the search does not know, and none twice', () => {
    for (const s of SECTIONS) {
      const known = new Set(SEARCH_INDEX.filter(e => e.section === s).map(e => e.key))
      const seen = new Set<string>()
      for (const d of subsOf(s)) for (const k of d.keys) {
        expect(known.has(k), `${s}/${d.id}: ${k}`).toBe(true)
        expect(seen.has(k), `${s}: ${k} is in two sub-tabs`).toBe(false)
        seen.add(k)
      }
    }
  })

  it('shows every key of the settings record in the sub-tab that edits it, and that one only', () => {
    const edited = new Map<string, string>()
    for (const [section, subs] of Object.entries(SUB_KEYS)) {
      for (const [sub, keys] of Object.entries(subs)) {
        for (const k of keys as readonly string[]) {
          expect(edited.has(`${section}/${k}`), `${section}: ${k} is edited by two sub-tabs`).toBe(false)
          edited.set(`${section}/${k}`, sub)
          const entry = SEARCH_INDEX.find(e => e.section === section && e.key === k)
          expect(entry, `${section}/${k} is not in the search`).toBeTruthy()
          expect(entry!.sub, `${section}/${k}`).toBe(sub)
        }
      }
    }
  })

  it("puts each group override next to the global setting it overrides, once", () => {
    // Only these categories are edited from this page; geo and risk belong to the risk center.
    const here = new Set(['2fa', 'notify', 'emergency', 'login', 'sub'])
    const seen = new Map<string, string>()
    for (const s of SECTIONS) {
      for (const d of subsOf(s) as readonly SubDef[]) {
        if (!d.scope) continue
        for (const k of d.scope.keys ?? []) expect(SCOPE_KEYS.some(m => m.key === k), `${s}/${d.id}: ${k}`).toBe(true)
        const shown = SCOPE_KEYS.filter(m => d.scope!.categories.includes(m.cat) && (!d.scope!.keys || d.scope!.keys.includes(m.key)))
        for (const m of shown) {
          expect(seen.has(m.key), `${m.key} can be overridden from two sub-tabs`).toBe(false)
          seen.set(m.key, `${s}/${d.id}`)
          // The audience means the same thing everywhere: the override stands where the global value stands.
          expect(d.keys, `${m.key}'s global setting ${String(m.field)} is not in ${s}/${d.id}`).toContain(m.field)
        }
      }
    }
    for (const m of SCOPE_KEYS.filter(x => here.has(x.cat))) expect(seen.has(m.key), `${m.key} cannot be overridden from any sub-tab`).toBe(true)
  })

  it('has only sections that exist', () => {
    expect(Object.keys(TREE).sort()).toEqual([...SECTIONS].sort() as SectionKey[])
  })
})
