import { describe, expect, it } from 'vitest'

import type { DestPolicy } from '@/api/accessControl'
import { emptyForm, formOf, inputOf } from './policyForm'

const policy = (over: Partial<DestPolicy> = {}): DestPolicy => ({
  id: 7, name: 'No mail', description: ' note ', action: 'block', state: 'report', list_ids: [9, 4, 6], inline: { cidrs: ['10.0.0.0/8', '192.168.0.0/16'], ports: '25,465', network: 'tcp', protocols: ['bittorrent'], private: true },
  scope: 'groups', group_ids: [3, 1], panel_ids: [5], priority: 1, template_key: '', created_at: 0, updated_at: 111, list_states: [], note: null,
  scope_missing: false, hits_recent: null, would_block_recent: null, last_hit_at: null, counts_as_risk: true, ...over,
})

describe('the policy request body', () => {
  it('is exactly the old editor\'s body, with list and group order kept as chosen', () => {
    expect(inputOf(formOf(policy()), { id: 7, updatedAt: 111 })).toEqual({
      name: 'No mail', description: 'note', action: 'block', state: 'report', list_ids: [9, 4, 6],
      inline: { cidrs: ['10.0.0.0/8', '192.168.0.0/16'], ports: '25,465', network: 'tcp', protocols: ['bittorrent'], private: true },
      scope: 'groups', group_ids: [3, 1], panel_ids: [], counts_as_risk: true, id: 7, updated_at: 111,
    })
  })

  it('leaves out what is empty: no inline keys, no template key, no ids of the scope not chosen, no risk flag on allow', () => {
    const form = { ...emptyForm('allow'), name: ' Office ', private: true, scope: 'all' as const, groupIds: [2], panelIds: [3] }
    expect(inputOf(form)).toEqual({
      name: 'Office', description: '', action: 'allow', state: 'off', list_ids: [], inline: { private: true }, scope: 'all', group_ids: [], panel_ids: [],
    })
  })

  it('carries a catalog template\'s key and a panel scope\'s ids', () => {
    const body = inputOf(formOf(policy({ template_key: 'bittorrent', scope: 'panels', group_ids: [] })))
    expect(body.template_key).toBe('bittorrent')
    expect(body.panel_ids).toEqual([5])
    expect(body.group_ids).toEqual([])
  })
})
