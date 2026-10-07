import { describe, expect, it } from 'vitest'
import { destinationListAvailable, policyListAvailable } from './destinationListAvailability'
import { destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'
import { accessVerdict } from './accessControl'

describe('cached destination content availability', () => {
  it('retains nonempty remote content through failed/ongoing refresh, but not before first download', () => {
    expect(destinationListAvailable({ kind: 'remote', entry_count: 1, last_fetched_at: 1000 })).toBe(true)
    expect(destinationListAvailable({ kind: 'geosite', entry_count: 1, last_fetched_at: null })).toBe(false)
    expect(destinationListAvailable({ kind: 'custom', entry_count: 1, last_fetched_at: null })).toBe(true)
    expect(destinationListAvailable({ kind: 'remote', entry_count: 0, last_fetched_at: 1000 })).toBe(false)
  })
  it('preserves the older overview contract while preferring explicit availability', () => {
    expect(policyListAvailable({ id: 1, name: 'List', state: 'ready' })).toBe(true)
    expect(policyListAvailable({ id: 1, name: 'List', state: 'pending' })).toBe(false)
    expect(policyListAvailable({ id: 1, name: 'List', state: 'refreshing', available: true })).toBe(true)
    expect(policyListAvailable({ id: 1, name: 'List', state: 'ready', available: false })).toBe(false)
  })
  it('does not claim cached entries become inactive while a refresh is in progress', () => {
    const policies = destinationPolicies()
    policies.block[0].list_states = [{ id: 1, name: 'List', state: 'refreshing', available: true }]
    expect(accessVerdict(destinationStatus(), policies)).toMatchObject({ kind: 'applied' })
    policies.block[0].list_states[0].available = false
    expect(accessVerdict(destinationStatus(), policies)).toMatchObject({ kind: 'list_pending' })
  })
})
