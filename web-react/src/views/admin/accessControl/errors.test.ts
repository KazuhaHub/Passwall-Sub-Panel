import { expect, it } from 'vitest'
import { destinationListReferences } from './errors'

const rejected = (used_by: unknown) => ({ isAxiosError: true, response: { status: 409, data: { error: 'dest_list_in_use', used_by } } })
it('keeps valid current references in order and rejects malformed navigation targets', () => {
  expect(destinationListReferences(rejected([
    { kind: 'policy', id: 12, name: 'No mail' }, { kind: 'policy', id: 12, name: 'Duplicate' },
    { kind: 'group', id: 12, name: 'Guests' }, { kind: 'policy', id: 13, name: '  ' },
    null, { kind: 'other', id: 14, name: 'Other' }, { kind: ['policy'], id: 14, name: 'Array' },
    { kind: 'policy', id: 0, name: 'Zero' }, { kind: 'policy', id: -1, name: 'Negative' },
    { kind: 'policy', id: 1.5, name: 'Fraction' }, { kind: 'policy', id: '14', name: 'String' },
    { kind: 'policy', id: Number.MAX_SAFE_INTEGER + 1, name: 'Unsafe' }, { kind: 'policy', id: 14 },
  ]))).toEqual([
    { kind: 'policy', id: 12, name: 'No mail' }, { kind: 'group', id: 12, name: 'Guests' },
    { kind: 'policy', id: 13, name: '#13' },
  ])
})
it.each([undefined, null, {}, 'policy'])('keeps missing or invalid reference arrays unknown (%j)', usedBy => {
  expect(destinationListReferences(rejected(usedBy))).toEqual([])
  expect(destinationListReferences(new Error('unavailable'))).toEqual([])
})
