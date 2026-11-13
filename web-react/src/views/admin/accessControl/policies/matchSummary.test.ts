import { describe, expect, it } from 'vitest'

import { matchSummary, summaryText } from './matchSummary'

const base = { listNames: [] as string[], cidrs: 0, private: false, bittorrent: false, ports: '', network: '' }
// A transparent t: the grammar, not the wording.
const t = (key: string, o?: Record<string, unknown>) => {
  const k = key.split('.').pop()!
  return k === 'or' ? ' | ' : k === 'and' ? ' & ' : `${k}:${o?.value ?? ''}`
}

describe('matchSummary', () => {
  it('a single list', () => {
    const s = matchSummary({ ...base, listNames: ['Internal'] })
    expect(summaryText(t, s)).toBe('list:Internal')
    expect(s.split).toBe(false)
  })
  it('a list with ports: no brackets for one alternative', () => {
    expect(summaryText(t, matchSummary({ ...base, listNames: ['Internal'], ports: '25, 465' }))).toBe('list:Internal & ports:25, 465')
  })
  it('BT with ports and network does not split: they are "and"', () => {
    const s = matchSummary({ ...base, bittorrent: true, ports: '6881', network: 'tcp' })
    expect(summaryText(t, s)).toBe('bt: & ports:6881 & network:TCP')
    expect(s.split).toBe(false)
  })
  it('BT with a list and ports splits, and brackets the alternatives', () => {
    const s = matchSummary({ ...base, bittorrent: true, listNames: ['Trackers'], ports: '1-2' })
    expect(summaryText(t, s)).toBe('(list:Trackers | bt:) & ports:1-2')
    expect(s.split).toBe(true)
  })
  it('IP ranges and private addresses are alternatives; nothing set reads empty', () => {
    expect(summaryText(t, matchSummary({ ...base, cidrs: 2, private: true }))).toBe('cidrs:2 | private:')
    expect(summaryText(t, matchSummary(base))).toBe('')
  })
})
