// @vitest-environment jsdom
import { expect, it } from 'vitest'
import { routeLineInput } from './vlessRoute'

it('writes optional match fields directly on the line and preserves regex commas', () => {
  const input = routeLineInput({ id: 'line', name: 'B', enabled: true, outbound_tag: 'direct', route: 1234, network: 'tcp', protocols: ['tls', 'http'], ip: [' 192.0.2.0/24 ', ''], domain: ['regexp:^a{1,3}\\.example$'], port: ' 443 ' })
  expect(input).toMatchObject({ network: 'tcp', protocols: ['http', 'tls'], ip: ['192.0.2.0/24'], domain: ['regexp:^a{1,3}\\.example$'], port: '443' })
  expect(input.route).toBeUndefined()
  expect(input).not.toHaveProperty('advanced_rules')
  expect(routeLineInput({ name: 'B', enabled: true, outbound_tag: 'direct', network: '', protocols: [], ip: [''], domain: [], port: ' ' })).toEqual(routeLineInput({ name: 'B', enabled: true, outbound_tag: 'direct' }))
})
