// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, mountAdmin } from '../adminTestHarness'
// Before the page: testSupport registers the mock of the link-probe card, which must precede the import that pulls it in.
import { echoPut, reads, uiSettings } from './testSupport'
import SettingsView from '../SettingsView'
import { SECTIONS } from './model'
import { SEARCH_INDEX } from './searchIndex'
import { subsOf } from './tree'

const selectedTab = () => screen.getByRole('tab', { selected: true }).textContent
const box = (name: string | RegExp) => screen.getByRole('textbox', { name }) as HTMLInputElement
const type = (name: string | RegExp, value: string) => fireEvent.change(box(name), { target: { value } })
// A sub-tab with unsaved changes carries the word in its name, so match on how it starts.
const goTo = (name: string) => fireEvent.click(screen.getByRole('tab', { name: new RegExp(`^${name}`) }))
const nav = (name: RegExp) => fireEvent.click(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name }))
const saveBar = () => within(screen.getByRole('region', { name: 'You have unsaved changes' }))

describe('sub-tab addresses', () => {
  it('opens the first sub-tab for a link that names only the section, which is every link from before sub-tabs', async () => {
    reads()
    mountAdmin(<SettingsView />, '/admin/settings?tab=security')
    await screen.findByText('Sessions')
    expect(selectedTab()).toBe('Sign-in and sessions')
    expect(screen.queryByText('Account lockout')).toBeNull()
  })

  it('reads a sub-tab this section does not have as its first', async () => {
    reads()
    mountAdmin(<SettingsView />, '/admin/settings?tab=security&sub=oidc')
    await screen.findByText('Sessions')
    expect(selectedTab()).toBe('Sign-in and sessions')
  })

  it('opens the sub-tab a link names, SSO providers included', async () => {
    reads()
    mountAdmin(<SettingsView />, '/admin/settings?tab=security&sub=twofa')
    await screen.findByText('Two-factor authentication (2FA)')
    expect(selectedTab()).toBe('Two-step verification')
    expect(screen.queryByText('Sessions')).toBeNull()
  })

  it('keeps the sub-tab in ?sub= (the first leaves it out), and a section change starts at the first', async () => {
    reads()
    const { router } = mountAdmin(<SettingsView />, '/admin/settings?tab=security')
    await screen.findByText('Sessions')
    goTo('Protection and rate limits')
    await screen.findByText('Account lockout')
    expect(router.state.location.search).toBe('?tab=security&sub=protection')
    goTo('Sign-in and sessions')
    await waitFor(() => expect(router.state.location.search).toBe('?tab=security'))
    goTo('Two-step verification')
    nav(/^Brand/)
    await screen.findByText('Site text')
    expect(router.state.location.search).toBe('?tab=brand')
    expect(selectedTab()).toBe('Name and appearance')
  })

  it('has no sub-tab bar for a section that is one screen', async () => {
    reads()
    mountAdmin(<SettingsView />, '/admin/settings?tab=legal')
    await screen.findByText('Privacy and terms page')
    expect(screen.queryByRole('tab')).toBeNull()
  })
})

describe('unsaved changes across sub-tabs', () => {
  it('keeps a draft when another sub-tab is shown, marks its sub-tab, and asks nothing', async () => {
    reads()
    mountAdmin(<SettingsView />, '/admin/settings?tab=security')
    await screen.findByText('Sessions')
    type('JWT issuer', 'other')
    goTo('Two-step verification')
    await screen.findByText('Two-factor authentication (2FA)')
    expect(screen.queryByRole('alertdialog')).toBeNull()
    // The dot (and its name) is on the sub-tab that holds the edit, and on the section in the list.
    expect(within(screen.getByRole('tab', { name: /Sign-in and sessions/ })).getByRole('img', { name: 'unsaved' })).toBeTruthy()
    expect(within(screen.getByRole('tab', { name: /Two-step verification/ })).queryByRole('img')).toBeNull()
    expect(within(screen.getByRole('navigation', { name: 'Settings sections' })).getByRole('button', { name: /Login & Security/ }).textContent).toContain('unsaved')
    goTo('Sign-in and sessions')
    expect(box('JWT issuer').value).toBe('other')
    expect(saveBar().getByRole('button', { name: 'Save' })).toBeTruthy()
  })

  it("saves only the sub-tab it is pressed in, and leaves another sub-tab's draft alone", async () => {
    reads()
    mountAdmin(<SettingsView />, '/admin/settings?tab=security')
    await screen.findByText('Sessions')
    type('JWT issuer', 'other')
    goTo('Two-step verification')
    fireEvent.click(await screen.findByRole('switch', { name: /authenticator app/i }))
    echoPut()
    fireEvent.click(saveBar().getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    const sent = api.put.mock.calls[0][1] as Record<string, unknown>
    expect(sent.totp_enabled).toBe(true)
    expect(sent.jwt_issuer).toBe(uiSettings().jwt_issuer)
    // The other draft is still there, unsaved.
    goTo('Sign-in and sessions')
    expect(box('JWT issuer').value).toBe('other')
  })

  it('asks once when the page is left, and names the sub-tabs with edits', async () => {
    reads()
    const { router } = mountAdmin(<SettingsView />, '/admin/settings?tab=security')
    await screen.findByText('Sessions')
    type('JWT issuer', 'other')
    goTo('Protection and rate limits')
    fireEvent.click(await screen.findByRole('switch', { name: /Enable failure lockout/ }))
    void router.navigate('/elsewhere')
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog.textContent).toContain('Login & Security · Sign-in and sessions, Login & Security · Protection and rate limits')
  })

  it('keeps a draft on a sub-tab that was opened by a link, not only by a click', async () => {
    reads()
    const { router } = mountAdmin(<SettingsView />, '/admin/settings?tab=security')
    await screen.findByText('Sessions')
    await act(async () => { await router.navigate('/admin/settings?tab=security&sub=protection', { replace: true }) })
    fireEvent.click(await screen.findByRole('switch', { name: /Enable failure lockout/ }))
    await act(async () => { await router.navigate('/admin/settings?tab=security', { replace: true }) })
    await act(async () => { await router.navigate('/admin/settings?tab=security&sub=twofa', { replace: true }) })
    await act(async () => { await router.navigate('/admin/settings?tab=security&sub=protection', { replace: true }) })
    expect(screen.getByRole('switch', { name: /Enable failure lockout/ }).getAttribute('aria-checked')).toBe('true')
  })
})

describe('every setting is on the page exactly once', () => {
  it('shows each setting of the search index in its own sub-tab, and in no other', async () => {
    // A MaxMind source shows its edition and token boxes; a setting behind a switch that is off is named by `via` and skipped.
    reads({}, { geo_ip_update_source: 'maxmind' })
    const { router } = mountAdmin(<SettingsView />, '/admin/settings')
    await screen.findByText('Runtime tunables')
    const home = (place: string) => document.querySelector(`[data-subtab="${place}"]`) as HTMLElement
    for (const section of SECTIONS) {
      for (const def of subsOf(section)) {
        const place = `${section}/${def.id}`
        const first = def.id === subsOf(section)[0].id
        await act(async () => { await router.navigate(`/admin/settings?tab=${section}${first ? '' : `&sub=${def.id}`}`, { replace: true }) })
        await waitFor(() => expect(home(place), place).toBeTruthy())
        for (const e of SEARCH_INDEX.filter(x => x.section === section && x.sub === def.id)) {
          if (section === 'legal') { expect(home(place).closest('#settings-section-legal')).toBeTruthy(); continue }
          if (e.via) continue
          await waitFor(() => expect(home(place).querySelector(`#${e.id}`), `${place}: ${e.key}`).toBeTruthy(), { timeout: 3000 })
        }
      }
    }
    // Everything is built now: each control of the index stands in one place, the sub-tab the tree gives it.
    for (const e of SEARCH_INDEX) {
      if (e.section === 'legal') continue
      const found = [...document.querySelectorAll(`#${e.id}`)]
      expect(found.length, `${e.section}/${e.key} appears ${found.length} times`).toBeLessThanOrEqual(1)
      for (const el of found) expect(el.closest('[data-subtab]')?.getAttribute('data-subtab'), `${e.section}/${e.key}`).toBe(`${e.section}/${e.sub}`)
    }
  }, 60_000)
})
