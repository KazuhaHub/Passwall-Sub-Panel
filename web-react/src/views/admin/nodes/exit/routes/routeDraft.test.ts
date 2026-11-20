// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import type { RouteView } from '@/api/routes'
import { draftIssues, draftOf, emptyRoute, inputOf, isDirty, moveRoute, portsValid, targetKey, targetOf } from './routeDraft'

const saved: RouteView[] = [
  { id: 'r1', name: 'ads', target: { kind: 'server', panel_id: 4 }, list_ids: [3], domains: ['domain:example.com', 'keyword:ad'], cidrs: ['10.0.0.0/8'], ports: '25,465', network: 'tcp', entries: 5, sent: true, notes: [] },
  { id: 'r2', target: { kind: 'local' }, list_ids: [], domains: ['full:a.example.com'], cidrs: [], ports: '', network: '', entries: 1, sent: true, notes: [] },
]

describe('route draft', () => {
  it('round-trips saved routes into the same input, keeping the ids', () => {
    const input = inputOf(draftOf(saved))
    expect(input[0]).toEqual({ id: 'r1', name: 'ads', target: { kind: 'server', panel_id: 4 }, list_ids: [3], domains: ['domain:example.com', 'keyword:ad'], cidrs: ['10.0.0.0/8'], ports: '25,465', network: 'tcp' })
    expect(input[1]).toEqual({ id: 'r2', target: { kind: 'local' }, list_ids: [], domains: ['full:a.example.com'], cidrs: [], ports: '', network: '' })
  })

  it('sends a new route without an id and without an empty name', () => {
    const d = [{ ...emptyRoute(), target: targetKey.local, listIds: [1], name: '  ' }]
    const [r] = inputOf(d)
    expect('id' in r).toBe(false)
    expect('name' in r).toBe(false)
  })

  it('is dirty only when something changed, reorder included', () => {
    const d = draftOf(saved)
    expect(isDirty(d, saved)).toBe(false)
    expect(isDirty(moveRoute(d, 0, 1), saved)).toBe(true)
    expect(isDirty(d.slice(0, 1), saved)).toBe(true)
    const edited = draftOf(saved)
    edited[1].ports = '80'
    expect(isDirty(edited, saved)).toBe(true)
    // Whitespace the server would not keep is not a change.
    const spaced = draftOf(saved)
    spaced[0].ports = '25, 465'
    spaced[0].domains += '\n\n'
    expect(isDirty(spaced, saved)).toBe(false)
  })

  it('moves a row and ignores a position outside the list', () => {
    expect(moveRoute([1, 2, 3], 0, 2)).toEqual([2, 3, 1])
    expect(moveRoute([1, 2, 3], 2, 1)).toEqual([1, 3, 2])
    expect(moveRoute([1, 2, 3], 0, 3)).toEqual([1, 2, 3])
    expect(moveRoute([1, 2, 3], -1, 1)).toEqual([1, 2, 3])
  })

  it('reads a target key both ways', () => {
    expect(targetOf('local')).toEqual({ kind: 'local' })
    expect(targetOf('server:9')).toEqual({ kind: 'server', panel_id: 9 })
    expect(targetOf('')).toBeNull()
    expect(targetOf('server:')).toBeNull()
  })

  it('has no issue for complete routes and names each problem otherwise', () => {
    expect(draftIssues(draftOf(saved))).toEqual([])
    const bad = [
      { ...emptyRoute() },
      { ...emptyRoute(), target: 'local', domains: 'regexp:^a\nexample.com\ndomain:ok.com', ports: '70000', name: 'x'.repeat(65) },
    ]
    const issues = draftIssues(bad)
    expect(issues).toEqual(expect.arrayContaining([
      { code: 'target', route: 1 }, { code: 'no_match', route: 1 },
      { code: 'regexp', route: 2, entry: 'regexp:^a' }, { code: 'entry', route: 2, entry: 'example.com' },
      { code: 'ports', route: 2 }, { code: 'name_long', route: 2 },
    ]))
    expect(issues.some(i => i.code === 'entry' && i.entry === 'domain:ok.com')).toBe(false)
  })

  it('counts the bytes of a name, not its characters', () => {
    const d = [{ ...emptyRoute(), target: 'local', network: 'udp' as const, name: '路'.repeat(22) }]
    expect(draftIssues(d)).toEqual([{ code: 'name_long', route: 1 }])
  })

  it('accepts ports and ranges and refuses malformed ones', () => {
    for (const ok of ['', '25', '25,465,587', '6881-6889', ' 80, 443 ']) expect(portsValid(ok), ok).toBe(true)
    for (const bad of ['0', '65536', '9-1', 'a', '1-', '1,,2', '-5']) expect(portsValid(bad), bad).toBe(false)
  })
})
