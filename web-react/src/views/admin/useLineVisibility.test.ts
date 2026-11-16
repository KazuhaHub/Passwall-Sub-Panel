// @vitest-environment jsdom
import { expect, it } from 'vitest'
import { lineVisibilityHint } from './useLineVisibility'

const data = { groups: [{ id: 1, name: 'VIP' }, { id: 2, name: 'Staff' }], node_groups: [1, 2], relays: [[2, 1], []], routes: [[1]] }

it('resolves group names per line and keeps "none" distinct from "unknown"', () => {
  expect(lineVisibilityHint({ status: 'ready', data }, 'relays', 0)).toEqual({ state: 'ready', groups: ['Staff', 'VIP'] })
  expect(lineVisibilityHint({ status: 'ready', data }, 'relays', 1)).toEqual({ state: 'ready', groups: [] })
  expect(lineVisibilityHint({ status: 'ready', data }, 'routes', 0)).toEqual({ state: 'ready', groups: ['VIP'] })
  expect(lineVisibilityHint({ status: 'error' }, 'relays', 0)).toEqual({ state: 'error' })
  expect(lineVisibilityHint({ status: 'loading' }, 'relays', 0)).toEqual({ state: 'loading' })
  expect(lineVisibilityHint({ status: 'idle' }, 'relays', 0)).toBeUndefined()
  // A row the answer does not cover yet (just added) is still loading, not "none".
  expect(lineVisibilityHint({ status: 'ready', data }, 'relays', 5)).toEqual({ state: 'loading' })
})
