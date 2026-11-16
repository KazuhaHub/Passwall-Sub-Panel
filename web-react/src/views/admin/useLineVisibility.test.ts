// @vitest-environment jsdom
import { expect, it } from 'vitest'
import { lineVisibilityHint, nodeGroupsHint } from './useLineVisibility'

const data = { groups: [{ id: 1, name: 'VIP' }, { id: 2, name: 'Staff' }], node_groups: [1, 2], saved_node_groups: [1, 2, 3], relays: [[2, 1], []], routes: [[1]] }

it('resolves group names per line and keeps "none" distinct from "unknown"', () => {
  expect(lineVisibilityHint({ status: 'ready', data }, 'relays', 0)).toEqual({ state: 'ready', items: ['Staff', 'VIP'] })
  expect(lineVisibilityHint({ status: 'ready', data }, 'relays', 1)).toEqual({ state: 'ready', items: [] })
  expect(lineVisibilityHint({ status: 'ready', data }, 'routes', 0)).toEqual({ state: 'ready', items: ['VIP'] })
  expect(lineVisibilityHint({ status: 'error' }, 'relays', 0)).toEqual({ state: 'error' })
  expect(lineVisibilityHint({ status: 'loading' }, 'relays', 0)).toEqual({ state: 'loading' })
  expect(lineVisibilityHint({ status: 'idle' }, 'relays', 0)).toBeUndefined()
  // A row the answer does not cover yet (just added) is still loading, not "none".
  expect(lineVisibilityHint({ status: 'ready', data }, 'relays', 5)).toEqual({ state: 'loading' })
})

it('names the node\'s groups and those a save would remove it from', () => {
  expect(nodeGroupsHint({ status: 'ready', data })).toEqual({ now: { state: 'ready', items: ['VIP', 'Staff'] }, removed: ['#3'] })
  expect(nodeGroupsHint({ status: 'error' })).toEqual({ now: { state: 'error' }, removed: [] })
})
