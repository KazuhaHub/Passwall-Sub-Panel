import { describe, expect, it } from 'vitest'
import type { Cert } from '@/api/certs'
import type { Node } from '@/api/types'
import { buildCredentials, certHealth, nodesByCert, parseDomains, sameDomains, sortCerts } from './model'

const NOW = Date.parse('2026-11-20T00:00:00Z')
const day = (n: number) => new Date(NOW + n * 86_400_000).toISOString()
const cert = (over: Partial<Cert>): Cert => ({
  id: 1, name: 'c', domains: [], status: 'active', acme_account_id: 1, dns_credential_id: 1, not_before: null, not_after: day(60),
  fingerprint: '', auto_renew: true, last_error: '', created_at: '', ...over,
})

describe('certHealth', () => {
  it('reads a failure before anything else, and an expired one as expired', () => {
    expect(certHealth(cert({ status: 'failed', not_after: day(60) }), NOW, 30).id).toBe('failed')
    expect(certHealth(cert({ status: 'expired', not_after: day(-3) }), NOW, 30)).toMatchObject({ id: 'expired', days: -3 })
    // Stored as active but already past its end between two list reads.
    expect(certHealth(cert({ status: 'active', not_after: day(-1) }), NOW, 30).id).toBe('expired')
  })
  it('warns inside the renewal threshold and is quiet outside it', () => {
    expect(certHealth(cert({ not_after: day(12) }), NOW, 30)).toMatchObject({ id: 'expiring', days: 12 })
    expect(certHealth(cert({ not_after: day(30) }), NOW, 30).id).toBe('expiring')
    expect(certHealth(cert({ not_after: day(31) }), NOW, 30).id).toBe('valid')
  })
  it('says issuing for a certificate that has not been issued yet', () => {
    expect(certHealth(cert({ status: 'pending', not_after: null }), NOW, 30)).toMatchObject({ id: 'issuing', days: null })
  })
})

describe('sortCerts', () => {
  it('puts problems first, then the soonest to expire', () => {
    const list = [
      cert({ id: 1, name: 'fine', not_after: day(80) }),
      cert({ id: 2, name: 'soon', not_after: day(5) }),
      cert({ id: 3, name: 'broken', status: 'failed' }),
      cert({ id: 4, name: 'issuing', status: 'pending', not_after: null }),
      cert({ id: 5, name: 'gone', status: 'expired', not_after: day(-9) }),
    ]
    expect(sortCerts(list, NOW, 30).map(c => c.name)).toEqual(['gone', 'broken', 'soon', 'issuing', 'fine'])
  })
})

describe('domains', () => {
  it('splits on lines, spaces and commas, and ignores order when comparing', () => {
    expect(parseDomains('*.a.test,\n a.test  b.test\n')).toEqual(['*.a.test', 'a.test', 'b.test'])
    expect(sameDomains(['a', 'b'], ['b', 'a'])).toBe(true)
    expect(sameDomains(['a'], ['a', 'b'])).toBe(false)
  })
})

describe('nodesByCert', () => {
  it('counts only nodes that deploy a managed certificate', () => {
    const n = (id: number, cert_source: Node['cert_source'], cert_id?: number) => ({ id, cert_source, cert_id }) as Node
    const m = nodesByCert([n(1, 'psp_managed', 7), n(2, 'manual', 7), n(3, 'psp_managed', 7), n(4, 'psp_managed', 0)])
    expect([...m.keys()]).toEqual([7])
    expect(m.get(7)?.map(x => x.id)).toEqual([1, 3])
  })
})

describe('buildCredentials', () => {
  const fields = [{ key: 'TOKEN', label: 'Token', secret: true }, { key: 'ZONE', label: 'Zone', secret: false, optional: true }]
  it('creating: requires every required value and leaves out a blank optional one', () => {
    expect(buildCredentials({ editing: false, custom: false, fields, values: { TOKEN: 'x' }, pairs: [] })).toEqual({ ok: true, credentials: { TOKEN: 'x' } })
    expect(buildCredentials({ editing: false, custom: false, fields, values: {}, pairs: [] })).toEqual({ ok: false })
  })
  it('editing: sends every field, a blank one meaning keep the stored secret', () => {
    expect(buildCredentials({ editing: true, custom: false, fields, values: { ZONE: 'z' }, pairs: [] })).toEqual({ ok: true, credentials: { TOKEN: '', ZONE: 'z' } })
  })
  it('custom: pairs with a key, blank values refused only when creating', () => {
    const pairs = [{ k: ' A ', v: '1' }, { k: '', v: 'ignored' }, { k: 'B', v: '' }]
    expect(buildCredentials({ editing: false, custom: true, fields: [], values: {}, pairs })).toEqual({ ok: false })
    expect(buildCredentials({ editing: true, custom: true, fields: [], values: {}, pairs })).toEqual({ ok: true, credentials: { A: '1', B: '' } })
  })
})
