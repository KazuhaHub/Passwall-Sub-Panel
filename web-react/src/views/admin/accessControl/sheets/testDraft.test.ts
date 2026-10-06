import { expect, it } from 'vitest'
import { testDraftInput, testTarget } from './testDraft'
it('keeps host-only URLs and IP tests distinct without DNS', () => {
  expect(testTarget(' https://SUB.example.test:8443/private?q=secret ')).toEqual({ target: 'sub.example.test', url: true, ip: false })
  expect(testTarget('192.0.2.7')).toEqual({ target: '192.0.2.7', url: false, ip: true })
  expect(testTarget('2001:db8::1')).toEqual({ target: '2001:db8::1', url: false, ip: true })
  expect(testTarget('EXAMPLE.test.').target).toBe('example.test')
})
it.each(['', 'https://name:password@example.test/path', 'ftp://example.test/file', 'https://example.test:65536/', 'bad host', '-bad.test', 'a..test', 'http://', '2001:db8::1%eth0', 'host/path'])('rejects invalid targets %s', target => {
  expect(testTarget(target).target).toBeNull()
})
it('requires integer ports and sends optional positive identities only', () => {
  for (const port of ['', '0', '65536', '1.5', '1e2', '-1']) expect(testDraftInput('example.test', port, 'tcp', null, null)).toBeNull()
  expect(testDraftInput('https://example.test/private', '443', 'udp', 13, 2)).toEqual({ target: 'example.test', port: 443, network: 'udp', user_id: 13, panel_id: 2 })
  expect(testDraftInput('example.test', '1', 'tcp', null, null)).toEqual({ target: 'example.test', port: 1, network: 'tcp' })
})
