// @vitest-environment jsdom
import { screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'

import { renderAccess, setTestLang } from '@/test/accessControlHarness'
import { useSiteStore } from '@/stores/site'
import SiteFooter from './SiteFooter'

function set(footerText: string, legalEnabled: boolean) { useSiteStore.setState({ footerText, legalEnabled }) }

beforeEach(() => set('© Example', false))

describe('SiteFooter', () => {
  it('is the footer text alone while the terms are off, and nothing when that is empty too', () => {
    const { container, unmount } = renderAccess(<SiteFooter />)
    expect(container.textContent).toBe('© Example')
    expect(screen.queryByRole('link')).toBeNull()
    unmount()
    set('', false)
    expect(renderAccess(<SiteFooter />).container.textContent).toBe('')
  })

  it('puts the two links after the footer text when the terms are on', () => {
    set('© Example', true)
    const { container } = renderAccess(<SiteFooter />)
    expect(container.textContent).toBe('© Example · 服务条款 · 隐私政策')
    expect(screen.getByRole('link', { name: '服务条款' }).getAttribute('href')).toBe('/legal/terms')
    expect(screen.getByRole('link', { name: '隐私政策' }).getAttribute('href')).toBe('/legal/privacy')
  })

  it('still renders, with the links alone, for a site whose footer text is empty', () => {
    set('', true)
    const { container } = renderAccess(<SiteFooter />)
    expect(container.textContent).toBe('服务条款 · 隐私政策')
  })

  it('in English', () => {
    setTestLang('en-US')
    set('', true)
    renderAccess(<SiteFooter />)
    expect(screen.getByRole('link', { name: 'Terms of Service' })).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Privacy Policy' })).toBeTruthy()
  })
})
