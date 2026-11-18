import { describe, expect, it } from 'vitest'
import { listenerLabel } from './listenerLabel'

const t = (key: string, values?: Record<string, unknown>) => `${key}|${values?.parent}|${values?.line}`

describe('listenerLabel', () => {
  it('names an ordinary listener by its node', () => {
    expect(listenerLabel(t, 'hk-reality')).toBe('hk-reality')
    expect(listenerLabel(t, 'hk-reality', { parent_name: 'HK', line_name: 'Mobile' })).toBe('hk-reality')
  })
  it('names a tunnel line\'s listener by its line and landing', () => {
    expect(listenerLabel(t, 'tunnel-77', { parent_node_id: 5, parent_name: 'HK-1', line_name: 'Mobile' }))
      .toBe('admin:servers.fronts.tunnel_listener|HK-1|Mobile')
  })
  it('falls back to the landing id when its name is missing', () => {
    expect(listenerLabel(t, 'tunnel-77', { parent_node_id: 5 })).toBe('admin:servers.fronts.tunnel_listener|#5|')
  })
})
