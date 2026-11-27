// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as legalApi from '@/api/legal'
import { mountAdmin } from '../adminTestHarness'
import { nothing, privacy, terms } from './legalTestFixtures'
import LegalTab from './LegalTab'

vi.mock('@/api/legal', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/legal')>()
  return { ...real, getAdminLegal: vi.fn(), publishLegal: vi.fn() }
})
vi.mock('@/i18n', () => ({ SUPPORTED_LANGUAGES: ['zh-CN', 'zh-TW', 'en-US'] }))

let served: Record<legalApi.LegalKind, legalApi.AdminLegal>
const onEnabled = vi.fn()
const onDisclose = vi.fn()

function mount(props: { enabled?: boolean; discloseUsage?: boolean } = {}) {
  return mountAdmin(<LegalTab enabled={props.enabled ?? true} onEnabledChange={onEnabled}
    discloseUsage={props.discloseUsage ?? false} onDiscloseUsageChange={onDisclose} />, '/admin/settings?tab=legal')
}
const card = (name: string) => screen.findByRole('region', { name })

beforeEach(() => {
  vi.unstubAllGlobals()
  served = { terms, privacy }
  onEnabled.mockReset(); onDisclose.mockReset()
  vi.mocked(legalApi.getAdminLegal).mockReset().mockImplementation(async k => served[k])
})

describe('Settings > Privacy and terms: the page', () => {
  it('shows the switch, the current consent version and what each document has published', async () => {
    mount()
    expect(await screen.findByText('Current consent version v3')).toBeTruthy()
    expect(screen.getByRole('switch', { name: 'Enable the privacy and terms page' })).toBeTruthy()
    const t = await card('Terms of Service')
    expect(within(t).getByText('zh-CN v4 · September 30')).toBeTruthy()
    expect(within(t).getByText('en-US v2 · September 12')).toBeTruthy()
    const p = await card('Privacy Policy')
    expect(within(p).getByText('zh-CN v3 · September 30')).toBeTruthy()
    expect(within(p).getByText('en-US not published (falls back to zh-CN)')).toBeTruthy()
  })

  it('the switch is the settings draft\'s: it reports the change and saves nothing itself', async () => {
    mount({ enabled: false })
    fireEvent.click(await screen.findByRole('switch', { name: 'Enable the privacy and terms page' }))
    expect(onEnabled).toHaveBeenCalledWith(true)
    expect(legalApi.publishLegal).not.toHaveBeenCalled()
  })

  it('shows the generated list the users would see, with its retention from the data', async () => {
    mount()
    const list = await screen.findByRole('region', { name: 'What we collect' })
    expect(within(list).getByText('kept for ever')).toBeTruthy()
    expect(within(list).getByText('4 nodes · kept 30 days')).toBeTruthy()
  })

  it('has the per-site usage switch off by default and reports its change', async () => {
    mount()
    const sw = await screen.findByRole('switch', { name: 'Disclose per-site usage in the privacy policy' })
    expect(sw.getAttribute('aria-checked')).toBe('false')
    expect(screen.getByText(/Off by default/)).toBeTruthy()
    fireEvent.click(sw)
    expect(onDisclose).toHaveBeenCalledWith(true)
  })

  it('warns when it is on but nothing is published, and offers to edit', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    const { router } = mount()
    expect(await screen.findByText(/Enabled, but no document is published yet/)).toBeTruthy()
    const notice = screen.getByText(/Enabled, but no document is published yet/).closest('[data-slot="notice"]') as HTMLElement
    fireEvent.click(within(notice).getByRole('button', { name: 'Edit' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/settings/legal/privacy'))
  })

  it('does not warn when it is off', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    mount({ enabled: false })
    await screen.findByText('Current consent version v0')
    expect(screen.queryByText(/no document is published yet/)).toBeNull()
  })

  it('a failed read says so instead of showing an empty page', async () => {
    vi.mocked(legalApi.getAdminLegal).mockRejectedValue(new Error('down'))
    mount()
    expect(await screen.findByText('Could not load this')).toBeTruthy()
  })
})


describe('opening the editor', () => {
  it('a document\'s Edit is a link to its own page, carrying this tab\'s address for the back link', async () => {
    const { router } = mount()
    const link = within(await card('Terms of Service')).getByRole('button', { name: 'Edit' })
    expect(link.getAttribute('href')).toBe('/admin/settings/legal/terms')
    fireEvent.click(link)
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/settings/legal/terms'))
    expect(router.state.location.state).toEqual({ from: '/admin/settings?tab=legal' })
  })
})

describe('the version history', () => {
  it('lists every version, newest first, with the material changes marked', async () => {
    mount()
    fireEvent.click(within(await card('Terms of Service')).getByRole('button', { name: 'Version history' }))
    const sheet = await screen.findByRole('dialog', { name: 'Terms of Service version history' })
    const rows = (await within(sheet).findAllByRole('listitem')).map(li => li.textContent)
    expect(rows).toEqual([
      expect.stringContaining('zh-CN · v4'), expect.stringContaining('en-US · v2'), expect.stringContaining('zh-CN · v3'),
    ])
    expect(rows[0]).toContain('Material change')
    expect(rows[1]).not.toContain('Material change')
  })

  it('says when nothing was ever published', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    mount()
    fireEvent.click(within(await card('Terms of Service')).getByRole('button', { name: 'Version history' }))
    expect(await screen.findByText('Nothing has been published yet')).toBeTruthy()
  })
})
