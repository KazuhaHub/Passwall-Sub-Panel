import { describe, expect, it } from 'vitest'
import { parseServerId, serverInstallPath, serverPath } from './serverRoutes'

describe('server addresses', () => {
  it('builds the server and installation pages', () => {
    expect(serverPath(7)).toBe('/admin/servers/7')
    expect(serverInstallPath(7)).toBe('/admin/servers/7/install')
  })
  it.each([['7', 7], ['007', 7]])('reads %s as %i', (raw, id) => expect(parseServerId(raw)).toBe(id))
  it.each([undefined, '', '0', '-3', '7.5', '07x', 'abc', 'new', '99999999999999999999'])('reads %s as no server', raw => {
    expect(parseServerId(raw)).toBe(0)
  })
})
