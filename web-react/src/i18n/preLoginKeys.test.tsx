// @vitest-environment jsdom
import { cleanup, render, waitFor } from '@testing-library/react'
import i18next from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'

import { I18N_STATIC_OPTIONS, flatten, type Nested } from './options'
import { BASE_NAMESPACES } from './resources'

// THE PAGES OUTSIDE THE SHELLS MUST NOT NEED `admin` OR `user`. Those two namespaces are no
// longer loaded at boot, so a pre-login screen that quietly read a key from one would show
// the key itself. Each screen is rendered with ONLY the boot namespaces registered, in
// both languages, and every key it asks for must resolve (missingKeyHandler), no text on the
// page may look like a raw key, and nothing may have been read from admin or user.

vi.mock('@/api/auth', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/auth')>()
  return {
    ...real,
    getAuthMethods: vi.fn(async () => ({ local: true, login_mode: 'local_only', registration_enabled: true })),
  }
})

import LoginView from '@/views/LoginView'
import ForgotPasswordView from '@/views/ForgotPasswordView'
import RegisterView from '@/views/RegisterView'
import LoggedOutView from '@/views/LoggedOutView'
import SsoErrorView from '@/views/SsoErrorView'
import SsoNoAccountView from '@/views/SsoNoAccountView'
import NotFoundView from '@/views/NotFoundView'

const files = import.meta.glob<{ default: Nested }>('../locales/*/*.json', { eager: true })
const RAW_KEY = /^([a-z_]+:)?[a-z0-9_]+(\.[a-z0-9_]+)+$/

const screens: Record<string, () => React.ReactElement> = {
  login: () => <LoginView />,
  'login (local)': () => <LoginView forceLocal />,
  'forgot password': () => <ForgotPasswordView />,
  register: () => <RegisterView />,
  'logged out': () => <LoggedOutView />,
  'sso error': () => <SsoErrorView />,
  'sso no account': () => <SsoNoAccountView />,
  'not found': () => <NotFoundView home="/login" />,
}

afterEach(cleanup)

describe.each(['zh-CN', 'en-US'])('pre-login screens in %s with only the boot namespaces loaded', lang => {
  const missing: string[] = []
  const instance = i18next.createInstance()

  beforeAll(async () => {
    const resources: Record<string, Record<string, Record<string, string>>> = { [lang]: {} }
    for (const ns of BASE_NAMESPACES) resources[lang][ns] = flatten(files[`../locales/${lang}/${ns}.json`].default)
    await instance.init({
      ...I18N_STATIC_OPTIONS,
      resources,
      lng: lang,
      initAsync: false,
      saveMissing: true,
      missingKeyHandler: (_lngs, ns, key) => { missing.push(`${ns}:${key}`) },
      react: { useSuspense: false },
    })
  })

  it.each(Object.keys(screens))('%s asks for no key that is not loaded', async name => {
    missing.length = 0
    const { container } = render(
      <I18nextProvider i18n={instance}><MemoryRouter initialEntries={['/x?error=no_account']}>{screens[name]()}</MemoryRouter></I18nextProvider>,
    )
    await waitFor(() => expect(container.textContent?.length).toBeGreaterThan(0))
    await new Promise(r => setTimeout(r, 30))

    expect(missing, `keys the ${name} screen could not resolve`).toEqual([])
    const texts = [...container.querySelectorAll('*')].flatMap(el => [...el.childNodes].filter(n => n.nodeType === 3).map(n => n.textContent!.trim())).filter(Boolean)
    expect(texts.filter(t => RAW_KEY.test(t)), 'text that is a raw key').toEqual([])
    expect(instance.hasResourceBundle(lang, 'admin') || instance.hasResourceBundle(lang, 'user')).toBe(false)
  })
})
