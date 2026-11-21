import { describe, expect, it } from 'vitest'
import type { Front } from '@/api/fronts'
import { frontGraph, type GraphWords } from './frontGraph'

const words: GraphWords = {
  columns: ['Clients connect to', 'SNI in the handshake', 'Goes to'],
  port: p => `Port ${p}`,
  routes: n => `${n} routes`,
  other: 'Other / no SNI',
  listener: (id, lb) => ({ label: `node ${id}`, sublabel: lb ? `127.0.0.1:${lb}` : undefined }),
  forward: (n, l) => `line ${n}/${l}`,
  link: id => `tunnel ${id}`,
  external: (h, p) => `${h}:${p}`,
}

const front = (over: Partial<Front> = {}): Front => ({
  id: 1, port: 443,
  listeners: [{ node_id: 5, loopback_port: 30001 }],
  routes: [
    { server_names: ['a.example.com', 'b.example.com'], target: { kind: 'listener', node_id: 5 } },
    { server_names: ['c.example.com'], target: { kind: 'forward', node_id: 7, line_id: 'x' } },
  ],
  default: { kind: 'external', host: 'www.example.com', port: 443 },
  ...over,
})

describe('frontGraph', () => {
  it('draws port, SNI and target as three columns with one chain per route, the default included', () => {
    const g = frontGraph([front()], new Set(), words)
    expect(g.columns.map(c => c.title)).toEqual(['Clients connect to', 'SNI in the handshake', 'Goes to'])
    expect(g.columns[0].boxes.map(b => [b.label, b.sublabel])).toEqual([['Port 443', '3 routes']])
    expect(g.columns[1].boxes.map(b => b.label)).toEqual(['a.example.com, b.example.com', 'c.example.com', 'Other / no SNI'])
    expect(g.columns[2].boxes.map(b => b.label)).toEqual(['node 5', 'line 7/x', 'www.example.com:443'])
    expect(g.columns[2].boxes[0].sublabel).toBe('127.0.0.1:30001')
    expect(g.edges).toHaveLength(6)
    expect(new Set(g.edges.map(e => e.id)).size).toBe(6)
  })

  it('draws a target used twice once, with a line from each route', () => {
    const f = front({ routes: [
      { server_names: ['a.example.com'], target: { kind: 'listener', node_id: 5 } },
      { server_names: ['b.example.com'], target: { kind: 'listener', node_id: 5 } },
    ] })
    const g = frontGraph([f], new Set(), words)
    expect(g.columns[2].boxes.filter(b => b.label === 'node 5')).toHaveLength(1)
    expect(g.edges.filter(e => e.to === 'target:listener:5')).toHaveLength(2)
  })

  it('keeps two fronts apart and marks the one that is not being sent', () => {
    const g = frontGraph([front(), front({ id: 2, port: 8443 })], new Set([2]), words)
    expect(g.columns[0].boxes.map(b => [b.label, b.status])).toEqual([['Port 443', undefined], ['Port 8443', 'warning']])
    // Same ids never repeat across fronts: boxes and edges are keyed by front.
    const ids = [...g.columns.flatMap(c => c.boxes.map(b => b.id)), ...g.edges.map(e => e.id)]
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('names a tunnel target by its tunnel', () => {
    const f = front({ routes: [{ server_names: ['t.example.com'], target: { kind: 'link', link_id: 9 } }] })
    expect(frontGraph([f], new Set(), words).columns[2].boxes[0].label).toBe('tunnel 9')
  })
})
