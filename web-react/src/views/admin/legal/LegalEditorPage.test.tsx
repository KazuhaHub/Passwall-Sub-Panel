// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as legalApi from '@/api/legal'
import * as settingsApi from '@/api/settings'
import ConfirmHost from '@/components/ConfirmHost'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { toast } from '../adminTestHarness'
import LegalEditorRoute from './LegalEditorRoute'
import { nothing, privacy, privacyText, terms, ver } from './legalTestFixtures'

vi.mock('@/api/legal', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/legal')>()
  return { ...real, getAdminLegal: vi.fn(), publishLegal: vi.fn(), getAffectedUsers: vi.fn() }
})
vi.mock('@/api/settings', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/settings')>()
  return { ...real, getUISettings: vi.fn() }
})
vi.mock('@/i18n', () => ({ SUPPORTED_LANGUAGES: ['zh-CN', 'zh-TW', 'en-US'] }))
// CodeMirror needs layout jsdom does not have; the stub keeps what the editor asks of it: a value, a change, and insertLine.
vi.mock('@/components/CodeEditor', async () => {
  const { forwardRef, useImperativeHandle } = await import('react')
  return {
    default: forwardRef(function CodeEditorStub({ value, onChange }: { value: string; onChange: (value: string) => void }, ref) {
      useImperativeHandle(ref, () => ({ revealLine: vi.fn(), insertLine: (text: string) => onChange(value.trim() === '' ? text : `${value}\n${text}`) }))
      return <textarea aria-label="code" value={value} onChange={e => onChange(e.target.value)} />
    }),
  }
})

let served: Record<legalApi.LegalKind, legalApi.AdminLegal>
let disclose = false

/** The editor at its address, with the settings page behind it so a back link has somewhere to land. */
function mount(kind = 'privacy', state?: unknown) {
  const router = createMemoryRouter([
    { path: '/admin/settings', element: <p>settings page</p> },
    { path: '/admin/settings/legal/:kind', element: <LegalEditorRoute /> },
  ], { initialEntries: [{ pathname: `/admin/settings/legal/${kind}`, state }] })
  const view = render(<QueryClientProvider client={makeTestQueryClient()}><ConfirmHost /><RouterProvider router={router} /></QueryClientProvider>)
  return { router, ...view }
}
async function openEditor(kind = 'privacy', title = 'Edit Privacy Policy') {
  const view = mount(kind)
  await screen.findByRole('heading', { name: title })
  await screen.findByLabelText('code')
  return view
}
const code = () => screen.getByLabelText('code') as HTMLTextAreaElement
const type = (v: string) => fireEvent.change(code(), { target: { value: v } })
const page = () => within(document.body)
const publishButton = () => screen.getByRole('button', { name: 'Publish…' })
const asPhone = () => vi.stubGlobal('matchMedia', (q: string) => ({ matches: /max-width/.test(q), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))

beforeEach(() => {
  vi.unstubAllGlobals()
  disclose = false
  served = { terms, privacy }
  vi.mocked(legalApi.getAdminLegal).mockReset().mockImplementation(async k => served[k])
  vi.mocked(legalApi.getAffectedUsers).mockReset().mockResolvedValue({ consent_version: 3, affected_users: 1240, accepted_current: 10 })
  vi.mocked(legalApi.publishLegal).mockReset().mockResolvedValue({ document: ver(9, 'zh-CN', 4, '2026-10-01T08:00:00Z'), consent_version: 3, consent_bumped: false })
  vi.mocked(settingsApi.getUISettings).mockReset().mockImplementation(async () => ({ legal_disclose_usage: disclose }) as settingsApi.UISettings)
})

describe('the address', () => {
  it('opens the document by its kind, with the back link to the settings tab', async () => {
    await openEditor()
    expect(screen.getByRole('link', { name: /System settings/ }).getAttribute('href')).toBe('/admin/settings?tab=legal')
    expect(legalApi.getAdminLegal).toHaveBeenCalledWith('privacy', expect.anything())
  })

  it('the back link returns to the address the person came from', async () => {
    mount('terms', { from: '/admin/settings?tab=legal&q=x' })
    await screen.findByRole('heading', { name: 'Edit Terms of Service' })
    expect(screen.getByRole('link', { name: /System settings/ }).getAttribute('href')).toBe('/admin/settings?tab=legal&q=x')
  })

  it('anything but terms or privacy is "no such document", without a request', async () => {
    for (const bad of ['nope', 'Privacy', 'new']) {
      const { unmount } = mount(bad)
      expect(await screen.findByText('This document does not exist')).toBeTruthy()
      unmount()
    }
    expect(legalApi.getAdminLegal).not.toHaveBeenCalled()
  })
})

describe('the editor', () => {
  it('opens on the published text of zh-CN, with the byte count', async () => {
    await openEditor()
    expect(code().value).toBe(privacyText)
    expect(page().getByText(/Published v3/)).toBeTruthy()
    expect(page().getByText(`${new TextEncoder().encode(privacyText).length} / 60,000 bytes`)).toBeTruthy()
  })

  it('a privacy policy that was never published starts from the marker skeleton, and nothing else is written for the owner', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    await openEditor()
    expect(code().value.trim()).toBe('[[data-collection]]')
  })

  it('and a terms document starts empty: the panel ships no legal text', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    await openEditor('terms', 'Edit Terms of Service')
    expect(code().value).toBe('')
  })

  it('a text over 60,000 bytes turns the count red, is named in the bar and cannot be published', async () => {
    await openEditor()
    type('a'.repeat(60001))
    expect(page().getByText('60,001 / 60,000 bytes').getAttribute('data-over')).toBe('true')
    const publish = publishButton()
    expect(publish.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(publish)
    expect(screen.queryByRole('dialog', { name: /Publish/ })).toBeNull()
    type('a'.repeat(60000))
    expect(page().getByText('60,000 / 60,000 bytes').getAttribute('data-over')).toBe('false')
    expect(publishButton().getAttribute('aria-disabled')).toBeNull()
  })

  it('hints that the list is missing from the text, and the insert button adds the marker once', async () => {
    await openEditor()
    const hint = 'The text has no "What we collect"; inserting it is recommended'
    expect(page().queryByText(hint)).toBeNull()
    type('Body only')
    expect(await page().findByText(hint)).toBeTruthy()
    fireEvent.click(page().getByRole('button', { name: 'Insert "What we collect"' }))
    expect(code().value).toBe('Body only\n[[data-collection]]')
    expect(page().queryByText(hint)).toBeNull()
    expect((page().getByRole('button', { name: 'Insert "What we collect"' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('previews through the page renderer after the typing pauses, never on a keystroke', async () => {
    await openEditor()
    expect(page().getByRole('heading', { name: 'Privacy' })).toBeTruthy()
    type('# New title')
    expect(page().queryByRole('heading', { name: 'New title' })).toBeNull()
    expect(await page().findByRole('heading', { name: 'New title' }, { timeout: 1500 })).toBeTruthy()
  })

  it('offers zh-CN and en-US; switching with unsaved edits asks first', async () => {
    await openEditor('terms', 'Edit Terms of Service')
    type('# Edited')
    const lang = page().getByRole('combobox', { name: 'Language' }) as HTMLSelectElement
    expect([...lang.options].map(o => o.value)).toEqual(['zh-CN', 'en-US'])
    fireEvent.change(lang, { target: { value: 'en-US' } })
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Switch language?')).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(code().value).toBe('# Edited')
    expect(lang.value).toBe('zh-CN')
    fireEvent.change(lang, { target: { value: 'en-US' } })
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(code().value).toBe('# Terms'))
  })

  it('switching language with nothing edited does not ask', async () => {
    await openEditor('terms', 'Edit Terms of Service')
    fireEvent.change(page().getByRole('combobox', { name: 'Language' }), { target: { value: 'en-US' } })
    await waitFor(() => expect(code().value).toBe('# Terms'))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('on a phone the editor and the preview are tabs, not side by side', async () => {
    asPhone()
    await openEditor()
    expect(page().getByRole('tab', { name: 'Edit' })).toBeTruthy()
    expect(screen.queryByLabelText('code')).toBeTruthy()
    expect(page().queryByRole('heading', { name: 'Privacy' })).toBeNull()
    fireEvent.click(page().getByRole('tab', { name: 'Preview' }))
    expect(await page().findByRole('heading', { name: 'Privacy' })).toBeTruthy()
    expect(screen.queryByLabelText('code')).toBeNull()
  })

  it('on a phone the preview tab has no editor to insert into, so the insert button is off there and on again in the edit tab', async () => {
    asPhone()
    await openEditor()
    type('Body only')
    const insert = () => page().getByRole('button', { name: 'Insert "What we collect"' }) as HTMLButtonElement
    expect(insert().disabled).toBe(false)
    fireEvent.click(page().getByRole('tab', { name: 'Preview' }))
    expect(insert().disabled).toBe(true)
    fireEvent.click(page().getByRole('tab', { name: 'Edit' }))
    expect(insert().disabled).toBe(false)
    fireEvent.click(insert())
    expect(code().value).toBe('Body only\n[[data-collection]]')
  })

  it('offers Publish only while the text differs from what is published, and Discard returns to it', async () => {
    await openEditor()
    expect(page().queryByRole('button', { name: 'Publish…' })).toBeNull()
    type(`${privacyText} more`)
    expect(publishButton()).toBeTruthy()
    fireEvent.click(page().getByRole('button', { name: 'Discard changes' }))
    expect(code().value).toBe(privacyText)
    expect(page().queryByRole('button', { name: 'Publish…' })).toBeNull()
  })

  const usageRow = { enabled: true, nodes: 2, retention_days: 7, includes_ip: false }
  const withUsage = (on: boolean): legalApi.AdminLegal => ({ ...privacy, data_collection: on ? { ...legalFixtureDc(), dest_usage: usageRow } : legalFixtureDc() })
  function legalFixtureDc() { return privacy.data_collection }

  it('the preview follows the saved "disclose usage" switch: switched off, the usage line is not in it', async () => {
    served = { terms, privacy: withUsage(true) }
    await openEditor()
    expect(page().getByRole('heading', { name: 'Privacy' })).toBeTruthy()
    expect(page().queryByText('Per-site usage')).toBeNull()
    expect(page().getByText('Access rule matches')).toBeTruthy()
  })

  it('switched on and listed, the usage line is in the preview and there is no hint', async () => {
    disclose = true
    served = { terms, privacy: withUsage(true) }
    await openEditor()
    expect(await page().findByText('Per-site usage')).toBeTruthy()
    expect(page().queryByText('The preview has no per-site usage line')).toBeNull()
  })

  it('switched on but not in the saved list yet, the preview says why the line is missing', async () => {
    disclose = true
    served = { terms, privacy: withUsage(false) }
    await openEditor()
    expect(await page().findByText('The preview has no per-site usage line')).toBeTruthy()
    expect(page().queryByText('Per-site usage')).toBeNull()
  })

  it('no hint while the switch is off', async () => {
    served = { terms, privacy: withUsage(false) }
    await openEditor()
    expect(page().getByRole('heading', { name: 'Privacy' })).toBeTruthy()
    expect(page().queryByText('The preview has no per-site usage line')).toBeNull()
  })
})

describe('leaving the page', () => {
  it('with edits, asks before another page opens, and stays when cancelled', async () => {
    const { router } = await openEditor()
    type('x')
    void router.navigate('/admin/settings')
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Discard unsaved changes?')).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(router.state.location.pathname).toBe('/admin/settings/legal/privacy')
    expect(code().value).toBe('x')
  })

  it('with edits, leaves once the discard is confirmed', async () => {
    const { router } = await openEditor()
    type('x')
    void router.navigate('/admin/settings')
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/settings'))
  })

  it('clean, leaves without asking', async () => {
    const { router } = await openEditor()
    await router.navigate('/admin/settings')
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(router.state.location.pathname).toBe('/admin/settings')
  })
})

describe('the version history', () => {
  it('opens from the page header as a dialog, newest first, with the material changes marked', async () => {
    await openEditor('terms', 'Edit Terms of Service')
    fireEvent.click(page().getByRole('button', { name: 'Version history' }))
    const dlg = await screen.findByRole('dialog', { name: 'Terms of Service version history' })
    const rows = (await within(dlg).findAllByRole('listitem')).map(li => li.textContent)
    expect(rows).toEqual([
      expect.stringContaining('zh-CN · v4'), expect.stringContaining('en-US · v2'), expect.stringContaining('zh-CN · v3'),
    ])
    expect(rows[0]).toContain('Material change')
  })
})

describe('publishing', () => {
  async function toPublish(text = `${privacyText}added`) {
    const view = await openEditor()
    type(text)
    fireEvent.click(publishButton())
    return { ...view, pub: await screen.findByRole('dialog', { name: 'Publish Privacy Policy (zh-CN) version 4?' }) }
  }
  const major = (pub: HTMLElement) => within(pub).getByRole('radio', { name: 'Material change: ask users to accept again' })

  it('names the document, locale and next version, and how much the text changed; a minor change is the default', async () => {
    const { pub } = await toPublish()
    expect(within(pub).getByText('5 characters added, 0 removed since the last version')).toBeTruthy()
    expect(major(pub).getAttribute('aria-checked')).toBe('false')
    expect(within(pub).getByRole('radio', { name: 'Minor change' }).getAttribute('aria-checked')).toBe('true')
    expect(within(pub).getByRole('button', { name: 'Publish' })).toBeTruthy()
  })

  it('a material change says how many ordinary users will be asked and when, and the button says it too', async () => {
    const { pub } = await toPublish()
    fireEvent.click(major(pub))
    expect(await within(pub).findByText(/About 1,240 ordinary users/)).toBeTruthy()
    // Users are asked when they next open the portal, not at a sign-in (a session can be days old).
    expect(within(pub).getByText(/next time they open the portal/)).toBeTruthy()
    expect(within(pub).getByText('You are about to ask every ordinary user to accept this document again.')).toBeTruthy()
    expect(within(pub).getByRole('button', { name: 'Publish and ask users to accept again' })).toBeTruthy()
  })

  it('publishes the text with the choice as made, says so, and returns to the settings without asking', async () => {
    const { pub, router } = await toPublish()
    fireEvent.click(major(pub))
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish and ask users to accept again' }))
    await waitFor(() => expect(legalApi.publishLegal).toHaveBeenCalledWith('privacy', { locale: 'zh-CN', content: `${privacyText}added`, consent_bump: true }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Version 4 published', 'success'))
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/settings'))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('an unmarked publication sends consent_bump false', async () => {
    const { pub } = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(legalApi.publishLegal).toHaveBeenCalledWith('privacy', expect.objectContaining({ consent_bump: false })))
  })

  it('a version conflict says another administrator published, reloads, and keeps the draft', async () => {
    vi.mocked(legalApi.publishLegal).mockRejectedValue(Object.assign(new Error('409'), { response: { status: 409, data: {} } }))
    const { pub, router } = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Another administrator just published a new version; reloaded', 'warning'))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Publish Privacy/ })).toBeNull())
    expect(code().value).toBe(`${privacyText}added`)
    expect(router.state.location.pathname).toBe('/admin/settings/legal/privacy')
    expect(vi.mocked(legalApi.getAdminLegal).mock.calls.length).toBeGreaterThan(1)
  })

  it('cannot be closed with Escape while the request is running', async () => {
    let release!: () => void
    vi.mocked(legalApi.publishLegal).mockReturnValue(new Promise(res => { release = () => res({ document: ver(9, 'zh-CN', 4, '2026-10-01T08:00:00Z'), consent_version: 3, consent_bumped: false }) }))
    const { pub } = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(legalApi.publishLegal).toHaveBeenCalled())
    fireEvent.keyDown(pub, { key: 'Escape' })
    expect(screen.getByRole('dialog', { name: /Publish Privacy/ })).toBeTruthy()
    release()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Publish Privacy/ })).toBeNull())
  })

  it('any other failure is said in the dialog, which stays', async () => {
    vi.mocked(legalApi.publishLegal).mockRejectedValue(new Error('boom'))
    const { pub } = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish' }))
    expect(await within(pub).findByText('Could not publish. Please try again.')).toBeTruthy()
    expect(screen.getByRole('dialog', { name: /Publish Privacy/ })).toBeTruthy()
  })
})
