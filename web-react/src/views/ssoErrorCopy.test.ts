import { describe, it, expect } from 'vitest'

import zhCN from '../locales/zh-CN/auth.json'
import enUS from '../locales/en-US/auth.json'
import { SSO_ERROR_KEYS } from './SsoErrorView'

// The codes the Go side can put in the failure redirect's `error` parameter.
// This list is the cross-language pin: the handler's page codes live in
// internal/transport/http/handler/auth_saml.go (samlPageAuthFailed,
// samlPageConfig, samlPageUnavailable), and the account-state codes come from
// the EnsureSSO result. If a code is added there without copy here, a person
// whose sign-in failed sees the raw code instead of a sentence.
const CODES_THE_BACKEND_SENDS = [
  'auth_failed',
  'saml_config',
  'saml_unavailable',
  'account_disabled',
  'account_pending',
  'sso_conflict',
  'sso_error',
]

describe('SSO failure page copy', () => {
  it.each(CODES_THE_BACKEND_SENDS)('has copy for %s in both locales', (code) => {
    const keys = SSO_ERROR_KEYS[code]
    expect(keys, `no key mapping for the code ${code}`).toBeDefined()
    for (const bundle of [zhCN, enUS]) {
      for (const key of [keys.title, keys.message]) {
        const value = (bundle as Record<string, string>)[key]
        expect(value, `missing ${key}`).toBeTruthy()
      }
    }
  })

  // The default is what an unrecognized code falls back to, so it has to exist
  // too — otherwise a code this build predates renders as a raw key.
  it('has copy for the fallback', () => {
    const keys = SSO_ERROR_KEYS.__default
    for (const bundle of [zhCN, enUS]) {
      for (const key of [keys.title, keys.message]) {
        expect((bundle as Record<string, string>)[key], `missing ${key}`).toBeTruthy()
      }
    }
  })

  // A description in the URL would let an IdP's own words reach the page, and it
  // would override the localized copy. The handler no longer sends one; this
  // keeps the view from starting to read one again.
  it('does not render a description from the query string', async () => {
    const source = await import('node:fs').then((fs) =>
      fs.readFileSync(new URL('./SsoErrorView.tsx', import.meta.url), 'utf8'))
    expect(source).not.toContain("params.get('description')")
  })
})
