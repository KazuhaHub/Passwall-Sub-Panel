import { describe, expect, it } from 'vitest'

import type { ProxyGroupMember } from '@/api/rules'
import { appendUniqueProxyGroupMember, applyProxyGroupOrder, defaultProxyGroupOptions, proxyGroupMemberIdentity, proxyGroupMemberListsEqual, proxyGroupOptionsEqual, proxyGroupOrderEqual, reorderProxyGroupMembers, reorderProxyGroupNames } from './proxyGroupMembers'

describe('proxy group member editor helpers', () => {
  const node: ProxyGroupMember = { kind: 'node', node_id: 42 }
  const direct: ProxyGroupMember = { kind: 'builtin', value: 'DIRECT' }
  const remaining: ProxyGroupMember = { kind: 'node_set', value: 'remaining' }

  it('moves a concrete node before DIRECT without mutating the source', () => {
    const source = [direct, node, remaining]
    expect(reorderProxyGroupMembers(source, 1, 0)).toEqual([node, direct, remaining])
    expect(source).toEqual([direct, node, remaining])
  })

  it('does not append a duplicate typed reference', () => {
    const source = [node]
    expect(appendUniqueProxyGroupMember(source, { kind: 'node', node_id: 42 })).toBe(source)
  })

  it('keeps node IDs and selector values distinct in identities', () => {
    expect(proxyGroupMemberIdentity(node)).toBe('node::42')
    expect(proxyGroupMemberIdentity(remaining)).toBe('node_set:remaining:0')
  })

  it('detects unsaved member changes including order and restoring defaults', () => {
    expect(proxyGroupMemberListsEqual([direct, node], [{ ...direct }, { ...node }])).toBe(true)
    expect(proxyGroupMemberListsEqual([node, direct], [direct, node])).toBe(false)
    expect(proxyGroupMemberListsEqual(undefined, undefined)).toBe(true)
    expect(proxyGroupMemberListsEqual(undefined, [])).toBe(false)
  })

  it('creates type-specific defaults and detects unsaved option changes', () => {
    expect(defaultProxyGroupOptions('select')).toEqual({ type: 'select' })
    expect(defaultProxyGroupOptions('url-test')).toMatchObject({ type: 'url-test', interval: 300, lazy: true, timeout: 5000, tolerance: 50 })
    expect(defaultProxyGroupOptions('load-balance')).toMatchObject({ type: 'load-balance', strategy: 'consistent-hashing' })
    const baseline = defaultProxyGroupOptions('fallback')
    expect(proxyGroupOptionsEqual(baseline, { ...baseline })).toBe(true)
    expect(proxyGroupOptionsEqual(baseline, { ...baseline, timeout: 3000 })).toBe(false)
    expect(proxyGroupOptionsEqual(undefined, baseline)).toBe(false)
  })

  it('applies partial group order and supports drag-style reordering', () => {
    const groups = [{ name: 'A' }, { name: 'B' }, { name: 'C' }]
    expect(applyProxyGroupOrder(groups, ['C', 'missing']).map(group => group.name)).toEqual(['A', 'B', 'C'])
    expect(reorderProxyGroupNames(['C', 'A', 'B'], 2, 0)).toEqual(['B', 'C', 'A'])
    expect(proxyGroupOrderEqual(['A', 'B'], ['A', 'B'])).toBe(true)
    expect(proxyGroupOrderEqual(['A', 'B'], ['B', 'A'])).toBe(false)
  })

  it('uses the project default order and prepends groups not in that order', () => {
    const groups = [
      { name: '🐟 漏网之鱼' },
      { name: '🏠 自定义代理组' },
      { name: '🍎 苹果服务' },
      { name: '🚀 节点选择' },
      { name: '🇨🇳 中国大陆' },
      { name: '🎮 UDP控制' },
    ]
    expect(applyProxyGroupOrder(groups, []).map(group => group.name)).toEqual([
      '🏠 自定义代理组',
      '🚀 节点选择',
      '🎮 UDP控制',
      '🇨🇳 中国大陆',
      '🍎 苹果服务',
      '🐟 漏网之鱼',
    ])
  })
})
