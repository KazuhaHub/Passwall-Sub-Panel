import { describe, expect, it } from 'vitest'
import type { ConnectionRecord, LiveConnection } from '@/api/riskCenter'
import { mergeConnections } from './mergeConnections'

const TAKEN = '2026-09-28T10:00:05Z'
const TAKEN_MS = Date.parse(TAKEN)

const SZ = { country_code: 'CN', country: 'China', region: 'Guangdong', region_code: 'GD', city: 'Shenzhen' }

function live(over: Partial<LiveConnection> = {}): LiveConnection {
  return {
    panel_id: 1, panel_name: 'P1', node: 'n1', source_key: '198.51.100.20', ip: '198.51.100.20',
    exclusion: '', seen_at: 1_790_000_000, region: SZ, devices: [], ...over,
  }
}

function hist(over: Partial<ConnectionRecord> = {}): ConnectionRecord {
  return {
    user_id: 7, upn: 'alice', display_name: 'Alice', panel_id: 1, panel_name: 'P1', node: 'n1',
    source_key: '198.51.100.20', ip: '198.51.100.20', exclusion: 'shared', region: null,
    first_seen_ms: 1_000, last_seen_ms: 2_000, count: 5, ...over,
  }
}

describe('mergeConnections', () => {
  it('a live-only entry is online and last seen at the snapshot, not the panel clock', () => {
    const [e, ...rest] = mergeConnections([live()], [], TAKEN)
    expect(rest).toEqual([])
    expect(e).toMatchObject({
      key: '1|n1|198.51.100.20', online: true, last_seen_ms: TAKEN_MS, first_seen_ms: 0, count: 0,
      seen_at: 1_790_000_000, exclusion: '', region: SZ,
    })
  })

  it('a history-only entry is offline and keeps its own times and count', () => {
    const [e] = mergeConnections([], [hist({ source_key: '203.0.113.9', ip: '203.0.113.9' })], TAKEN)
    expect(e).toMatchObject({
      key: '1|n1|203.0.113.9', online: false, first_seen_ms: 1_000, last_seen_ms: 2_000, count: 5,
      seen_at: 0, exclusion: 'shared', region: null,
    })
  })

  it('both: the live judgement and place win, history supplies first seen and count', () => {
    const [e, ...rest] = mergeConnections([live()], [hist()], TAKEN)
    expect(rest).toEqual([])
    expect(e).toMatchObject({
      online: true, exclusion: '', region: SZ, first_seen_ms: 1_000, count: 5,
      last_seen_ms: TAKEN_MS, seen_at: 1_790_000_000,
    })
  })

  it('both: last seen is the later of the history and the snapshot', () => {
    const later = TAKEN_MS + 60_000
    const [e] = mergeConnections([live()], [hist({ last_seen_ms: later })], TAKEN)
    expect(e.last_seen_ms).toBe(later)
  })

  it('the key includes the node: one source on two nodes is two entries', () => {
    const out = mergeConnections([live({ node: 'n1' })], [hist({ node: 'n2' })], TAKEN)
    expect(out.map(e => e.key)).toEqual(['1|n1|198.51.100.20', '1|n2|198.51.100.20'])
    expect(out.map(e => e.online)).toEqual([true, false])
  })

  it('orders online first, then by last seen, newest first', () => {
    const out = mergeConnections(
      [live({ source_key: 'b', ip: 'b' })],
      [
        hist({ source_key: 'old', ip: 'old', last_seen_ms: 1_000 }),
        hist({ source_key: 'new', ip: 'new', last_seen_ms: TAKEN_MS + 5_000 }),
        hist({ source_key: 'mid', ip: 'mid', last_seen_ms: 5_000 }),
      ],
      TAKEN,
    )
    expect(out.map(e => e.source_key)).toEqual(['b', 'new', 'mid', 'old'])
  })

  it('no snapshot time leaves a live entry with no last seen rather than a wrong one', () => {
    const [e] = mergeConnections([live()], [], null)
    expect(e.last_seen_ms).toBe(0)
  })
})
