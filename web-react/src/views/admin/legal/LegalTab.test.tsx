// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as legalApi from '@/api/legal'
import { mountAdmin, toast } from '../adminTestHarness'
import LegalTab from './LegalTab'

vi.mock('@/api/legal', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/legal')>()
  return { ...real, getAdminLegal: vi.fn(), publishLegal: vi.fn(), getAffectedUsers: vi.fn() }
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

const privacyText = '# Privacy\n\nThis service\n\n[[data-collection]]'
const dc: legalApi.DataCollection = {
  sub_log: { enabled: true, retention_days: 0, includes_ip: true },
  dest_hits: { enabled: true, nodes: 4, retention_days: 30, includes_ip: false },
}
const ver = (id: number, locale: string, version: number, at: string, bump = false) => ({ id, locale, version, consent_bump: bump, published_at: at, published_by: 1 })
const docOf = (v: ReturnType<typeof ver>, content: string) => ({ ...v, content })

const terms: legalApi.AdminLegal = {
  kind: 'terms', enabled: true, consent_version: 3, data_collection: dc,
  documents: [docOf(ver(5, 'zh-CN', 4, '2026-09-30T08:00:00Z'), '# 条款'), docOf(ver(4, 'en-US', 2, '2026-09-12T08:00:00Z'), '# Terms')],
  versions: [ver(5, 'zh-CN', 4, '2026-09-30T08:00:00Z', true), ver(4, 'en-US', 2, '2026-09-12T08:00:00Z'), ver(2, 'zh-CN', 3, '2026-09-01T08:00:00Z')],
}
const privacy: legalApi.AdminLegal = {
  kind: 'privacy', enabled: true, consent_version: 3, data_collection: dc,
  documents: [docOf(ver(3, 'zh-CN', 3, '2026-09-30T08:00:00Z'), privacyText)],
  versions: [ver(3, 'zh-CN', 3, '2026-09-30T08:00:00Z', true), ver(2, 'zh-CN', 2, '2026-09-20T08:00:00Z'), ver(1, 'zh-CN', 1, '2026-09-10T08:00:00Z')],
}
const nothing = (kind: legalApi.LegalKind): legalApi.AdminLegal => ({ kind, enabled: false, consent_version: 0, documents: [], versions: [], data_collection: dc })

let served: Record<legalApi.LegalKind, legalApi.AdminLegal>
const onEnabled = vi.fn()
const onDisclose = vi.fn()

function mount(props: { enabled?: boolean; discloseUsage?: boolean } = {}) {
  return mountAdmin(<LegalTab enabled={props.enabled ?? true} onEnabledChange={onEnabled}
    discloseUsage={props.discloseUsage ?? false} onDiscloseUsageChange={onDisclose} />, '/admin/settings?tab=legal')
}
const card = (name: string) => screen.findByRole('region', { name })
async function openEditor(kind = 'Privacy Policy') {
  fireEvent.click(within(await card(kind)).getByRole('button', { name: 'Edit' }))
  const dlg = await screen.findByRole('dialog', { name: `Edit ${kind}` })
  await screen.findByLabelText('code')
  return dlg
}
const code = () => screen.getByLabelText('code') as HTMLTextAreaElement
const type = (v: string) => fireEvent.change(code(), { target: { value: v } })
const publishButton = (root: HTMLElement) => within(root).getByRole('button', { name: 'Publish…' })
const asPhone = () => vi.stubGlobal('matchMedia', (q: string) => ({ matches: /max-width/.test(q), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))

beforeEach(() => {
  vi.unstubAllGlobals()
  served = { terms, privacy }
  onEnabled.mockReset(); onDisclose.mockReset()
  vi.mocked(legalApi.getAdminLegal).mockReset().mockImplementation(async k => served[k])
  vi.mocked(legalApi.getAffectedUsers).mockReset().mockResolvedValue({ consent_version: 3, affected_users: 1240, accepted_current: 10 })
  vi.mocked(legalApi.publishLegal).mockReset().mockResolvedValue({ document: ver(9, 'zh-CN', 4, '2026-10-01T08:00:00Z'), consent_version: 3, consent_bumped: false })
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
    mount()
    expect(await screen.findByText(/Enabled, but no document is published yet/)).toBeTruthy()
    const notice = screen.getByText(/Enabled, but no document is published yet/).closest('[data-slot="notice"]') as HTMLElement
    fireEvent.click(within(notice).getByRole('button', { name: 'Edit' }))
    expect(await screen.findByRole('dialog', { name: 'Edit Privacy Policy' })).toBeTruthy()
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

describe('the editor', () => {
  it('opens on the published text of zh-CN, with the byte count', async () => {
    mount()
    const dlg = await openEditor()
    expect(code().value).toBe(privacyText)
    expect(within(dlg).getByText(/Published v3/)).toBeTruthy()
    expect(within(dlg).getByText(`${new TextEncoder().encode(privacyText).length} / 60,000 bytes`)).toBeTruthy()
  })

  it('a privacy policy that was never published starts from the marker skeleton, and nothing else is written for the owner', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    mount()
    await openEditor()
    expect(code().value.trim()).toBe('[[data-collection]]')
  })

  it('and a terms document starts empty: the panel ships no legal text', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    mount()
    await openEditor('Terms of Service')
    expect(code().value).toBe('')
  })

  it('a text over 60,000 bytes turns the count red, is named in the bar and cannot be published', async () => {
    mount()
    const dlg = await openEditor()
    type('a'.repeat(60001))
    expect(within(dlg).getByText('60,001 / 60,000 bytes').getAttribute('data-over')).toBe('true')
    const publish = publishButton(dlg)
    expect(publish.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(publish)
    expect(screen.queryByRole('dialog', { name: /Publish/ })).toBeNull()
    type('a'.repeat(60000))
    expect(within(dlg).getByText('60,000 / 60,000 bytes').getAttribute('data-over')).toBe('false')
    expect(publishButton(dlg).getAttribute('aria-disabled')).toBeNull()
  })

  it('hints that the list is missing from the text, and the insert button adds the marker once', async () => {
    mount()
    const dlg = await openEditor()
    const hint = 'The text has no "What we collect"; inserting it is recommended'
    expect(within(dlg).queryByText(hint)).toBeNull()
    type('Body only')
    expect(await within(dlg).findByText(hint)).toBeTruthy()
    fireEvent.click(within(dlg).getByRole('button', { name: 'Insert "What we collect"' }))
    expect(code().value).toBe('Body only\n[[data-collection]]')
    expect(within(dlg).queryByText(hint)).toBeNull()
    expect((within(dlg).getByRole('button', { name: 'Insert "What we collect"' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('previews through the page renderer after the typing pauses, never on a keystroke', async () => {
    mount()
    const dlg = await openEditor()
    expect(within(dlg).getByRole('heading', { name: 'Privacy' })).toBeTruthy()
    type('# New title')
    expect(within(dlg).queryByRole('heading', { name: 'New title' })).toBeNull()
    expect(await within(dlg).findByRole('heading', { name: 'New title' }, { timeout: 1500 })).toBeTruthy()
  })

  it('offers zh-CN and en-US; switching with unsaved edits asks first', async () => {
    mount()
    const dlg = await openEditor('Terms of Service')
    type('# Edited')
    const lang = within(dlg).getByRole('combobox', { name: 'Language' }) as HTMLSelectElement
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

  it('closing with edits asks first; closing clean does not', async () => {
    mount()
    const dlg = await openEditor()
    fireEvent.click(within(dlg).getByRole('button', { name: 'Close details' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Edit Privacy Policy' })).toBeNull())
    const again = await openEditor()
    type('x')
    fireEvent.click(within(again).getByRole('button', { name: 'Close details' }))
    const ask = await screen.findByRole('alertdialog')
    expect(within(ask).getByText('Discard unsaved changes?')).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(screen.getByRole('dialog', { name: 'Edit Privacy Policy' })).toBeTruthy()
  })

  it('on a phone the editor and the preview are tabs, not side by side', async () => {
    asPhone()
    mount()
    const dlg = await openEditor()
    expect(within(dlg).getByRole('tab', { name: 'Edit' })).toBeTruthy()
    expect(screen.queryByLabelText('code')).toBeTruthy()
    expect(within(dlg).queryByRole('heading', { name: 'Privacy' })).toBeNull()
    fireEvent.click(within(dlg).getByRole('tab', { name: 'Preview' }))
    expect(await within(dlg).findByRole('heading', { name: 'Privacy' })).toBeTruthy()
    expect(screen.queryByLabelText('code')).toBeNull()
  })

  it('on a phone the preview tab has no editor to insert into, so the insert button is off there and on again in the edit tab', async () => {
    asPhone()
    mount()
    const dlg = await openEditor()
    type('Body only')
    const insert = () => within(dlg).getByRole('button', { name: 'Insert "What we collect"' }) as HTMLButtonElement
    expect(insert().disabled).toBe(false)
    fireEvent.click(within(dlg).getByRole('tab', { name: 'Preview' }))
    expect(insert().disabled).toBe(true)
    fireEvent.click(within(dlg).getByRole('tab', { name: 'Edit' }))
    expect(insert().disabled).toBe(false)
    fireEvent.click(insert())
    expect(code().value).toBe('Body only\n[[data-collection]]')
  })

  it('offers Publish only while the text differs from what is published, and Discard returns to it', async () => {
    mount()
    const dlg = await openEditor()
    expect(within(dlg).queryByRole('button', { name: 'Publish…' })).toBeNull()
    type(`${privacyText} more`)
    expect(publishButton(dlg)).toBeTruthy()
    fireEvent.click(within(dlg).getByRole('button', { name: 'Discard changes' }))
    expect(code().value).toBe(privacyText)
    expect(within(dlg).queryByRole('button', { name: 'Publish…' })).toBeNull()
  })

  const usageRow = { enabled: true, nodes: 2, retention_days: 7, includes_ip: false }
  const withUsage = (on: boolean): legalApi.AdminLegal => ({ ...privacy, data_collection: on ? { ...dc, dest_usage: usageRow } : dc })

  it('the preview follows the unsaved "disclose usage" switch: switched off, the usage line is not in it', async () => {
    served = { terms, privacy: withUsage(true) }
    mount({ discloseUsage: false })
    const dlg = await openEditor()
    expect(within(dlg).getByRole('heading', { name: 'Privacy' })).toBeTruthy()
    expect(within(dlg).queryByText('Per-site usage')).toBeNull()
    expect(within(dlg).getByText('Access rule matches')).toBeTruthy()
  })

  it('switched on and listed, the usage line is in the preview and there is no hint', async () => {
    served = { terms, privacy: withUsage(true) }
    mount({ discloseUsage: true })
    const dlg = await openEditor()
    expect(await within(dlg).findByText('Per-site usage')).toBeTruthy()
    expect(within(dlg).queryByText('The preview has no per-site usage line')).toBeNull()
  })

  it('switched on but not in the saved list yet, the preview says why the line is missing', async () => {
    served = { terms, privacy: withUsage(false) }
    mount({ discloseUsage: true })
    const dlg = await openEditor()
    expect(await within(dlg).findByText('The preview has no per-site usage line')).toBeTruthy()
    expect(within(dlg).queryByText('Per-site usage')).toBeNull()
  })

  it('no hint while the switch is off', async () => {
    served = { terms, privacy: withUsage(false) }
    mount({ discloseUsage: false })
    const dlg = await openEditor()
    expect(within(dlg).getByRole('heading', { name: 'Privacy' })).toBeTruthy()
    expect(within(dlg).queryByText('The preview has no per-site usage line')).toBeNull()
  })
})

describe('publishing', () => {
  async function toPublish(text = `${privacyText}added`) {
    mount()
    const dlg = await openEditor()
    type(text)
    fireEvent.click(publishButton(dlg))
    return screen.findByRole('dialog', { name: 'Publish Privacy Policy (zh-CN) version 4?' })
  }
  const major = (pub: HTMLElement) => within(pub).getByRole('radio', { name: 'Material change: ask users to accept again' })

  it('names the document, locale and next version, and how much the text changed; a minor change is the default', async () => {
    const pub = await toPublish()
    expect(within(pub).getByText('5 characters added, 0 removed since the last version')).toBeTruthy()
    expect(major(pub).getAttribute('aria-checked')).toBe('false')
    expect(within(pub).getByRole('radio', { name: 'Minor change' }).getAttribute('aria-checked')).toBe('true')
    expect(within(pub).getByRole('button', { name: 'Publish' })).toBeTruthy()
  })

  it('a material change says how many ordinary users will be asked and when, and the button says it too', async () => {
    const pub = await toPublish()
    fireEvent.click(major(pub))
    expect(await within(pub).findByText(/About 1,240 ordinary users/)).toBeTruthy()
    // Users are asked when they next open the portal, not at a sign-in (a session can be days old).
    expect(within(pub).getByText(/next time they open the portal/)).toBeTruthy()
    expect(within(pub).getByText('You are about to ask every ordinary user to accept this document again.')).toBeTruthy()
    expect(within(pub).getByRole('button', { name: 'Publish and ask users to accept again' })).toBeTruthy()
  })

  it('publishes the text with the choice as made, says so, and closes both dialogs', async () => {
    const pub = await toPublish()
    fireEvent.click(major(pub))
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish and ask users to accept again' }))
    await waitFor(() => expect(legalApi.publishLegal).toHaveBeenCalledWith('privacy', { locale: 'zh-CN', content: `${privacyText}added`, consent_bump: true }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Version 4 published', 'success'))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('an unmarked publication sends consent_bump false', async () => {
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(legalApi.publishLegal).toHaveBeenCalledWith('privacy', expect.objectContaining({ consent_bump: false })))
  })

  it('a version conflict says another administrator published, reloads, and keeps the draft', async () => {
    vi.mocked(legalApi.publishLegal).mockRejectedValue(Object.assign(new Error('409'), { response: { status: 409, data: {} } }))
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Another administrator just published a new version; reloaded', 'warning'))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Publish Privacy/ })).toBeNull())
    expect(code().value).toBe(`${privacyText}added`)
    expect(vi.mocked(legalApi.getAdminLegal).mock.calls.length).toBeGreaterThan(2)
  })

  it('cannot be closed with Escape while the request is running', async () => {
    let release!: () => void
    vi.mocked(legalApi.publishLegal).mockReturnValue(new Promise(res => { release = () => res({ document: ver(9, 'zh-CN', 4, '2026-10-01T08:00:00Z'), consent_version: 3, consent_bumped: false }) }))
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(legalApi.publishLegal).toHaveBeenCalled())
    fireEvent.keyDown(pub, { key: 'Escape' })
    expect(screen.getByRole('dialog', { name: /Publish Privacy/ })).toBeTruthy()
    release()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Publish Privacy/ })).toBeNull())
  })

  it('any other failure is said in the dialog, which stays', async () => {
    vi.mocked(legalApi.publishLegal).mockRejectedValue(new Error('boom'))
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: 'Publish' }))
    expect(await within(pub).findByText('Could not publish. Please try again.')).toBeTruthy()
    expect(screen.getByRole('dialog', { name: /Publish Privacy/ })).toBeTruthy()
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
