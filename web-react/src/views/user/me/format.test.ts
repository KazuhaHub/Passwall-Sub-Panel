import { describe, expect, it } from 'vitest'
import { buildImportURL, bytesToHuman, expiryOf, isSafeAppScheme, maskUrl } from './format'

const profile = { sub_url: 'https://sub.example.test/a b?x=1&y=é', upn: 'u@example.test' }

describe('buildImportURL', () => {
  const b64 = btoa(unescape(encodeURIComponent(profile.sub_url)))
  const fill = (template: string, over: object = {}) => buildImportURL({ ...profile, ...over }, { import_url_template: template })

  it('fills every placeholder form the way the old page did', () => {
    expect(fill('x://{{ sub_url_encoded }}')).toBe(`x://${encodeURIComponent(profile.sub_url)}`)
    expect(fill('x://{{ sub_url }}')).toBe(`x://${profile.sub_url}`)
    expect(fill('x://{{ sub_url_b64 }}')).toBe(`x://${b64}`)
    expect(fill('x://{{ sub_url_b64_url_safe }}')).toBe(`x://${b64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')}`)
  })

  it('names the profile from the server, then the display name, then the sign-in name', () => {
    expect(fill('{{ profile_name }}', { profile_name: 'From server', display_name: 'D' })).toBe('From server')
    expect(fill('{{ profile_name }}', { display_name: 'D' })).toBe('D')
    expect(fill('{{ profile_name_encoded }}', { profile_name: 'a b&c' })).toBe('a%20b%26c')
    expect(fill('{{ profile_name }}')).toBe('u@example.test')
  })

  it('gives the update interval in minutes and in hours, 24 hours when unset', () => {
    expect(fill('{{ sub_update_interval_minutes }}/{{ sub_update_interval_hours }}')).toBe('1440/24')
    expect(fill('{{ sub_update_interval_minutes }}/{{ sub_update_interval_hours }}', { sub_update_interval_hours: 6 })).toBe('360/6')
  })
})

describe('maskUrl', () => {
  it('keeps protocol and host and hides everything after, whatever its length', () => {
    expect(maskUrl('https://sub.example.test/short')).toBe(`https://sub.example.test/${'•'.repeat(16)}`)
    expect(maskUrl('https://sub.example.test/a-very-long-secret-token-indeed')).toBe(`https://sub.example.test/${'•'.repeat(16)}`)
    expect(maskUrl('not a url')).not.toContain('not')
  })
})

describe('schemes', () => {
  it('allows an app scheme and refuses script-capable and scheme-less links', () => {
    expect(isSafeAppScheme('clash://install-config?url=x')).toBe(true)
    for (const bad of ['javascript:alert(1)', 'JavaScript:1', 'data:text/html,x', 'vbscript:x', 'blob:x', 'file:///etc/passwd', 'no-scheme']) {
      expect(isSafeAppScheme(bad)).toBe(false)
    }
  })
})

describe('numbers and dates', () => {
  it('prints bytes as before, zero without a unit', () => {
    expect(bytesToHuman(0)).toBe('0')
    expect(bytesToHuman(1536)).toBe('1.50 KB')
    expect(bytesToHuman(5 * 1024 ** 3)).toBe('5.00 GB')
  })
  it('rounds the days left up and marks a past expiry', () => {
    const now = Date.parse('2026-06-01T12:00:00Z')
    expect(expiryOf('2026-06-02T00:00:00Z', now)).toMatchObject({ days: 1, expired: false })
    expect(expiryOf('2026-05-30T00:00:00Z', now)).toMatchObject({ days: -2, expired: true })
    expect(expiryOf(null)).toBeNull()
  })
})
