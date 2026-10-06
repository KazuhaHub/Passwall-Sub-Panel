import { describe, expect, it } from 'vitest'
import { destinationNode, destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'
import { accessVerdict, nodeAccessTone, nodeFilter, fallbackState, pipelineSteps, statusNeedsPolling } from './accessControl'

it('polls outstanding publication or candidate acknowledgement, including paused/empty candidates, then stops', () => {
  expect(statusNeedsPolling()).toBe(false)
  expect(statusNeedsPolling(destinationStatus())).toBe(false)
  expect(statusNeedsPolling(destinationStatus({ generation: 2 }))).toBe(true)
  expect(statusNeedsPolling(destinationStatus({ nodes: [destinationNode({ state: 'pending' })] }))).toBe(true)
  expect(statusNeedsPolling(destinationStatus({ paused: true, nodes: [destinationNode({ state: 'paused', minted_kind: 'paused', pending_since: 3000 })] }))).toBe(true)
  expect(statusNeedsPolling(destinationStatus({ nodes: [destinationNode({ state: 'none', minted_kind: 'empty', pending_since: null })] }))).toBe(false)
})

describe('access control verdict priority', () => {
  it.each([
    ['paused', { paused: true, publish_error: { kind: 'domains', used: 50001, limit: 50000 } }, 'failing'],
    ['publication', { publish_error: { kind: 'domains', used: 50001, limit: 50000 } }, 'failing'],
    ['rejected', { nodes: [destinationNode({ state: 'rejected' })] }, 'failing'],
    ['over_limit', { nodes: [destinationNode({ state: 'over_limit' })] }, 'failing'],
    ['sniffing', { nodes: [destinationNode({ state: 'sniffing' })] }, 'failing'],
    ['no_coverage', { nodes: [destinationNode({ kind: '3xui', state: 'unsupported_kind', supports: { policy: false, hits: false, usage: false } })] }, 'failing'],
    ['upgrade', { nodes: [destinationNode(), destinationNode({ state: 'unsupported_version' })] }, 'attention'],
    ['offline', { nodes: [destinationNode(), destinationNode({ state: 'offline' })] }, 'attention'],
    ['unpublished', { generation: 2, next_publish_at: 3000 }, 'measuring'],
    ['pending', { nodes: [destinationNode({ state: 'pending' })] }, 'measuring'],
    ['applied', {}, 'ok'],
  ] as const)('%s', (kind, change, tone) => {
    expect(accessVerdict(destinationStatus(change), destinationPolicies())).toMatchObject({ kind, tone })
  })
  it('keeps design-excluded panels in the detail rather than colouring a healthy fleet', () => {
    expect(accessVerdict(destinationStatus({ nodes: [destinationNode(), destinationNode({ kind: 'sui', state: 'unsupported_kind' })] }), destinationPolicies()))
      .toMatchObject({ kind: 'applied', tone: 'ok', done: 1, total: 1 })
  })
  it('counts a capable node awaiting its first candidate as future coverage', () => {
    expect(accessVerdict(destinationStatus({ generation: 2, nodes: [destinationNode({ state: 'none', applied_rules: 0 })] }), destinationPolicies()))
      .toMatchObject({ kind: 'unpublished', tone: 'measuring' })
  })
  it('shows quiet when no policy is enabled and never treats no data as a healthy verdict', () => {
    expect(accessVerdict(destinationStatus({ nodes: [] }), destinationPolicies({ block: [] })))
      .toMatchObject({ kind: 'quiet', tone: 'quiet' })
    expect(accessVerdict(destinationStatus({ nodes: [] }), undefined)).toBeNull()
  })
  it('prioritizes list failure/readiness over pending deployment', () => {
    const policies = destinationPolicies()
    policies.block[0].list_states = [{ id: 9, name: 'Fraud', state: 'failed' }]
    expect(accessVerdict(destinationStatus({ generation: 2 }), policies)).toMatchObject({ kind: 'list_failed', tone: 'attention', name: 'Fraud' })
    policies.block[0].list_states[0].state = 'pending'
    expect(accessVerdict(destinationStatus(), policies)).toMatchObject({ kind: 'list_pending', tone: 'attention' })
  })
})
it('classifies long-pending nodes and preserves the fixed stage-1c pipeline', () => {
  const pending = destinationNode({ state: 'pending', pending_since: 1000 })
  expect(nodeFilter(pending, 600999)).toBe('pending')
  expect(nodeFilter(pending, 601001)).toBe('problem')
  expect(nodeAccessTone(pending, 601001)).toBe('attention')
  expect(nodeAccessTone(destinationNode({ state: 'offline' }), 0)).toBe('inhibited')
  expect(pipelineSteps(false)).toEqual(['allow', 'exemption', 'block', 'observe', 'direct'])
  expect(pipelineSteps(true)).toEqual(['allow', 'exemption', 'block', 'group', 'observe', 'direct'])
})
it('distinguishes pending fallback, confirmed fallback and exhausted empty acknowledgement', () => {
  expect(fallbackState(destinationNode({ state: 'rejected', minted_kind: 'fallback', pending_since: 1000 }))).toBe('waiting')
  expect(fallbackState(destinationNode({ state: 'rejected', minted_kind: 'fallback' }))).toBe('applied')
  expect(fallbackState(destinationNode({ state: 'rejected', minted_kind: 'empty', fallback_exhausted: true, pending_since: 1000, applied_rules: 0 }))).toBe('stopping')
  expect(fallbackState(destinationNode({ state: 'rejected', minted_kind: 'empty', fallback_exhausted: true, applied_rules: 0 }))).toBe('exhausted')
})
