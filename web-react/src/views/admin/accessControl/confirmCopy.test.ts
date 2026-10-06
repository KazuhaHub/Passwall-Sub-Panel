import { expect, it } from 'vitest'
import { destinationPolicies } from '@/test/accessControlFixtures'
import { needsFirstPublishConfirm } from './confirmCopy'

it.each(['switch', 'editor', 'template', 'allowlist'] as const)('uses published facts at the %s entry', origin => {
  const pending = destinationPolicies({ published_has_access_control: false })
  const change = origin === 'allowlist' ? { mode: 'allowlist' as const } : { enabled: true }
  expect(needsFirstPublishConfirm(pending, [], change)).toBe(true)
  expect(needsFirstPublishConfirm({ ...pending, published_has_access_control: true }, [], change)).toBe(false)
})
it('does not ask for a disabled policy or open group and includes published allowlist-only history', () => {
  const empty = destinationPolicies({ published_has_access_control: false, block: [] })
  expect(needsFirstPublishConfirm(empty, [], { enabled: false })).toBe(false)
  expect(needsFirstPublishConfirm(empty, [], { mode: 'open' })).toBe(false)
  expect(needsFirstPublishConfirm({ ...empty, published_has_access_control: true }, [{ mode: 'allowlist' }], { enabled: true })).toBe(false)
})
