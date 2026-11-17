// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import type { Front, FrontOptions } from '@/api/fronts'
import { draftIssues, draftOf, inputOf, leaving, linesFor, listenersFor, newlyMoved, targetKey, type Draft } from './frontDraft'

const probe = { low: 31000, high: 31999 }
const describeTarget = (k: string) => `target ${k}`

const front: Front = {
  id: 1, port: 443,
  listeners: [{ node_id: 1, loopback_port: 30001 }],
  routes: [
    { server_names: ['a.example.com'], target: { kind: 'listener', node_id: 1 } },
    { server_names: ['land.example.com'], target: { kind: 'forward', node_id: 5, line_id: 'x' } },
  ],
  default: { kind: 'external', host: 'www.example.com', port: 443 },
}

const options: FrontOptions = {
  listeners: [
    { node_id: 1, name: 'hk', protocol: 'vless', port: 443, enabled: true, frontable: true, security: 'reality', server_names: ['a.example.com'] },
    { node_id: 2, name: 'ss', protocol: 'shadowsocks', port: 443, enabled: true, frontable: false, reason: 'no_sni', server_names: [] },
    { node_id: 3, name: 'other', protocol: 'vless', port: 8443, enabled: true, frontable: true, server_names: [] },
  ],
  lines: [
    { node_id: 5, node_name: 'jp', line_id: 'x', line_name: 'via hk', port: 443, enabled: true, server_names: ['land.example.com'] },
    { node_id: 6, node_name: 'us', line_id: 'y', line_name: 'elsewhere', port: 20443, enabled: true, server_names: [] },
  ],
}

describe('front draft', () => {
  it('a new draft starts at 443 with nothing chosen for the admin', () => {
    const d = draftOf(null, 443)
    expect(d).toMatchObject({ port: '443', listeners: [], routes: [], host: '', confirm: false })
  })

  it('round-trips a stored front into exactly the values it holds', () => {
    const input = inputOf(draftOf(front, 443))
    expect(input.port).toBe(443)
    expect(input.listeners).toEqual([{ node_id: 1, loopback_port: 30001 }])
    expect(input.routes.map(r => r.server_names)).toEqual([['a.example.com'], ['land.example.com']])
    expect(input.routes[1].target).toEqual({ kind: 'forward', node_id: 5, line_id: 'x' })
    expect(input.default).toEqual({ host: 'www.example.com', port: 443 })
  })

  it('has no issue for a complete draft and names each missing piece otherwise', () => {
    const ok = draftOf(front, 443)
    expect(draftIssues(ok, options, probe, describeTarget)).toEqual([])
    const bad: Draft = { ...ok, port: '31500', host: '', defaultPort: '0', listeners: [{ node_id: 1, loopback: '31001' }], routes: [{ key: 'k', names: '', target: '' }] }
    const codes = draftIssues(bad, options, probe, describeTarget).map(i => i.code)
    expect(codes).toEqual(expect.arrayContaining(['port', 'loopback_probe', 'route_target', 'route_names', 'listener_unrouted', 'default_host', 'default_port']))
  })

  it('refuses a name routed twice and says where it already goes', () => {
    const d = draftOf(front, 443)
    d.routes[1].names = 'A.example.com.'
    const issue = draftIssues(d, options, probe, describeTarget).find(i => i.code === 'name_twice')
    expect(issue).toMatchObject({ name: 'a.example.com', route: 2, owner: `target ${targetKey.listener(1)}` })
  })

  it('refuses a loopback port used twice or equal to the shared port', () => {
    const d = draftOf(front, 443)
    d.listeners.push({ node_id: 3, loopback: '30001' })
    d.routes.push({ key: 'z', names: 'z.example.com', target: targetKey.listener(3) })
    expect(draftIssues(d, options, probe, describeTarget).map(i => i.code)).toContain('loopback_twice')
    d.listeners[0].loopback = '443'
    expect(draftIssues(d, options, probe, describeTarget).map(i => i.code)).toContain('loopback')
  })

  it('only listeners that are not already behind this front need the move confirmed', () => {
    const d = draftOf(front, 443)
    expect(newlyMoved(d, front)).toEqual([])
    d.listeners.push({ node_id: 3, loopback: '30002' })
    expect(newlyMoved(d, front)).toEqual([3])
    expect(newlyMoved(d, null)).toEqual([1, 3])
  })

  it('lists the listeners and lines that sit on the chosen port', () => {
    const d = draftOf(null, 443)
    expect(listenersFor(options, 443, d).map(l => l.node_id)).toEqual([1, 2])
    expect(linesFor(options, 443).map(l => l.line_id)).toEqual(['x'])
    d.listeners.push({ node_id: 3, loopback: '' })
    expect(listenersFor(options, 443, d).map(l => l.node_id)).toEqual([1, 2, 3])
  })

  it('names what a save takes away from a front that exists', () => {
    const d = draftOf(front, 443)
    expect(leaving(d, front)).toEqual({ listeners: [], lines: [] })
    d.listeners = []
    d.routes = d.routes.filter(r => r.target.startsWith('forward'))
    expect(leaving(d, front)).toEqual({ listeners: [1], lines: [] })
    d.routes = []
    expect(leaving(d, front).lines).toEqual(['5/x'])
    const moved = draftOf(front, 443)
    moved.port = '8443'
    expect(leaving(moved, front)).toEqual({ listeners: [1], lines: ['5/x'] })
    expect(leaving(moved, null)).toEqual({ listeners: [], lines: [] })
  })
})
