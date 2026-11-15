// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as legalApi from '@/api/legal'
import { confirmMock, renderAccess, setTestLang, snack } from '@/test/accessControlHarness'
import LegalTab from './LegalTab'

vi.mock('@/api/legal', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/legal')>()
  return { ...real, getAdminLegal: vi.fn(), publishLegal: vi.fn(), getAffectedUsers: vi.fn() }
})

const privacyText = '# 隐私\n\n本服务\n\n[[data-collection]]'
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
  return renderAccess(<LegalTab enabled={props.enabled ?? true} onEnabledChange={onEnabled}
    discloseUsage={props.discloseUsage ?? false} onDiscloseUsageChange={onDisclose} />, { route: '/admin/settings?tab=legal' })
}
const card = (name: string) => screen.findByRole('region', { name })
async function openEditor(kind = '隐私政策') {
  fireEvent.click(within(await card(kind)).getByRole('button', { name: '编辑' }))
  const dlg = await screen.findByRole('dialog', { name: `编辑${kind}` })
  // CodeMirror is a lazy chunk.
  await screen.findByLabelText('code')
  return dlg
}
const code = () => screen.getByLabelText('code') as HTMLTextAreaElement
const type = (v: string) => fireEvent.change(code(), { target: { value: v } })
const asPhone = () => vi.stubGlobal('matchMedia', (q: string) => ({ matches: /max-width/.test(q), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))

beforeEach(() => {
  vi.unstubAllGlobals()
  served = { terms, privacy }
  onEnabled.mockReset(); onDisclose.mockReset()
  vi.mocked(legalApi.getAdminLegal).mockReset().mockImplementation(async k => served[k])
  vi.mocked(legalApi.getAffectedUsers).mockReset().mockResolvedValue({ consent_version: 3, affected_users: 1240, accepted_current: 10 })
  vi.mocked(legalApi.publishLegal).mockReset().mockResolvedValue({ document: ver(9, 'zh-CN', 4, '2026-10-01T08:00:00Z'), consent_version: 3, consent_bumped: false })
})

describe('Settings > Privacy and terms: the page (S21)', () => {
  it('shows the switch, the current consent version and what each document has published', async () => {
    mount()
    expect(await screen.findByText('当前同意版本 v3')).toBeTruthy()
    expect(screen.getByRole('switch', { name: '启用隐私与协议页' })).toBeTruthy()
    const t = await card('服务条款')
    expect(within(t).getByText('zh-CN v4 · 9月30日')).toBeTruthy()
    expect(within(t).getByText('en-US v2 · 9月12日')).toBeTruthy()
    const p = await card('隐私政策')
    expect(within(p).getByText('zh-CN v3 · 9月30日')).toBeTruthy()
    expect(within(p).getByText('en-US 尚未发布（回落到 zh-CN）')).toBeTruthy()
  })

  it('the switch is the settings draft\'s: it reports the change and saves nothing itself', async () => {
    mount({ enabled: false })
    fireEvent.click(await screen.findByRole('switch', { name: '启用隐私与协议页' }))
    expect(onEnabled).toHaveBeenCalledWith(true)
    expect(legalApi.publishLegal).not.toHaveBeenCalled()
  })

  it('shows the generated list the users would see, with its retention from the data', async () => {
    mount()
    const list = await screen.findByRole('region', { name: '我们收集什么' })
    expect(within(list).getByText('永久保留')).toBeTruthy()
    expect(within(list).getByText('4 台节点 · 保留 30 天')).toBeTruthy()
  })

  it('has the per-site usage switch off by default and reports its change', async () => {
    mount()
    const sw = await screen.findByRole('switch', { name: '在隐私政策中披露按网站用量' }) as HTMLInputElement
    expect(sw.checked).toBe(false)
    expect(screen.getByText(/默认关闭/)).toBeTruthy()
    fireEvent.click(sw)
    expect(onDisclose).toHaveBeenCalledWith(true)
  })

  it('warns when it is on but nothing is published, and offers to edit', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    mount()
    expect(await screen.findByText('已启用，但还没有发布任何文档：公开页会显示「此页面未启用」')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '去编辑' }))
    expect(await screen.findByRole('dialog', { name: '编辑隐私政策' })).toBeTruthy()
  })

  it('does not warn when it is off, or when something is published', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    mount({ enabled: false })
    await screen.findByText('当前同意版本 v0')
    expect(screen.queryByText(/还没有发布任何文档/)).toBeNull()
  })

  it('a failed read says so instead of showing an empty page', async () => {
    vi.mocked(legalApi.getAdminLegal).mockRejectedValue(new Error('down'))
    mount()
    expect(await screen.findByText('暂时无法加载')).toBeTruthy()
  })
})

describe('the editor (S21)', () => {
  it('opens on the published text of zh-CN, with the byte count', async () => {
    mount()
    const dlg = await openEditor()
    expect(code().value).toBe(privacyText)
    expect(within(dlg).getByText('当前已发布 v3（9月30日）')).toBeTruthy()
    expect(within(dlg).getByText(`${new TextEncoder().encode(privacyText).length} / 60,000 字节`)).toBeTruthy()
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
    await openEditor('服务条款')
    expect(code().value).toBe('')
  })

  it('a text over 60,000 bytes turns the count red and cannot be published', async () => {
    mount()
    const dlg = await openEditor()
    type('a'.repeat(60001))
    expect(within(dlg).getByText('60,001 / 60,000 字节').getAttribute('data-over')).toBe('true')
    expect((within(dlg).getByRole('button', { name: '发布…' }) as HTMLButtonElement).disabled).toBe(true)
    type('a'.repeat(60000))
    expect(within(dlg).getByText('60,000 / 60,000 字节').getAttribute('data-over')).toBe('false')
    expect((within(dlg).getByRole('button', { name: '发布…' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('hints that the list is missing from the text, and the insert button adds the marker once', async () => {
    mount()
    const dlg = await openEditor()
    expect(within(dlg).queryByText('正文里没有「我们收集什么」，建议插入')).toBeNull()
    type('只有正文')
    expect(await within(dlg).findByText('正文里没有「我们收集什么」，建议插入')).toBeTruthy()
    fireEvent.click(within(dlg).getByRole('button', { name: '插入「我们收集什么」' }))
    expect(code().value).toBe('只有正文\n[[data-collection]]')
    expect(within(dlg).queryByText('正文里没有「我们收集什么」，建议插入')).toBeNull()
    expect((within(dlg).getByRole('button', { name: '插入「我们收集什么」' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('previews through the page renderer after the typing pauses, never on a keystroke', async () => {
    mount()
    const dlg = await openEditor()
    expect(within(dlg).getByRole('heading', { name: '隐私' })).toBeTruthy()
    type('# 新标题')
    expect(within(dlg).queryByRole('heading', { name: '新标题' })).toBeNull()
    expect(await within(dlg).findByRole('heading', { name: '新标题' }, { timeout: 1500 })).toBeTruthy()
  })

  it('offers zh-CN and en-US; switching with unsaved edits asks first', async () => {
    mount()
    const dlg = await openEditor('服务条款')
    type('# 改过')
    const lang = within(dlg).getByRole('combobox', { name: '语言' })
    confirmMock.mockResolvedValueOnce(false)
    fireEvent.mouseDown(lang)
    fireEvent.click(await screen.findByRole('option', { name: 'en-US' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '切换语言？' })))
    expect(code().value).toBe('# 改过')
    confirmMock.mockResolvedValueOnce(true)
    fireEvent.mouseDown(lang)
    fireEvent.click(await screen.findByRole('option', { name: 'en-US' }))
    await waitFor(() => expect(code().value).toBe('# Terms'))
  })

  it('closing with edits asks first; closing clean does not', async () => {
    mount()
    const dlg = await openEditor()
    fireEvent.click(within(dlg).getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '编辑隐私政策' })).toBeNull())
    expect(confirmMock).not.toHaveBeenCalled()
    const again = await openEditor()
    type('改')
    confirmMock.mockResolvedValueOnce(false)
    fireEvent.click(within(again).getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的修改？' })))
    expect(screen.getByRole('dialog', { name: '编辑隐私政策' })).toBeTruthy()
  })

  it('on a phone the editor and the preview are tabs, not side by side', async () => {
    asPhone()
    mount()
    const dlg = await openEditor()
    expect(within(dlg).getByRole('tab', { name: '编辑' })).toBeTruthy()
    expect(screen.queryByLabelText('code')).toBeTruthy()
    expect(within(dlg).queryByRole('heading', { name: '隐私' })).toBeNull()
    fireEvent.click(within(dlg).getByRole('tab', { name: '预览' }))
    expect(await within(dlg).findByRole('heading', { name: '隐私' })).toBeTruthy()
    expect(screen.queryByLabelText('code')).toBeNull()
  })
})

describe('the editor, review follow-ups', () => {
  it('on a phone the preview tab has no editor to insert into, so the insert button is off there and on again in the edit tab', async () => {
    asPhone()
    mount()
    const dlg = await openEditor()
    type('只有正文')
    const insert = () => within(dlg).getByRole('button', { name: '插入「我们收集什么」' }) as HTMLButtonElement
    expect(insert().disabled).toBe(false)
    fireEvent.click(within(dlg).getByRole('tab', { name: '预览' }))
    expect(insert().disabled).toBe(true)
    fireEvent.click(within(dlg).getByRole('tab', { name: '编辑' }))
    expect(insert().disabled).toBe(false)
    fireEvent.click(insert())
    expect(code().value).toBe('只有正文\n[[data-collection]]')
  })

  const usageRow = { enabled: true, nodes: 2, retention_days: 7, includes_ip: false }
  const withUsage = (on: boolean): legalApi.AdminLegal => ({ ...privacy, data_collection: on ? { ...dc, dest_usage: usageRow } : dc })

  it('the preview follows the unsaved "disclose usage" switch: switched off, the usage line is not in it', async () => {
    served = { terms, privacy: withUsage(true) }
    mount({ discloseUsage: false })
    const dlg = await openEditor()
    expect(within(dlg).getByRole('heading', { name: '隐私' })).toBeTruthy()
    expect(within(dlg).queryByText('按网站用量')).toBeNull()
    expect(within(dlg).getByText('访问规则命中')).toBeTruthy()
  })

  it('switched on and listed, the usage line is in the preview and there is no hint', async () => {
    served = { terms, privacy: withUsage(true) }
    mount({ discloseUsage: true })
    const dlg = await openEditor()
    expect(await within(dlg).findByText('按网站用量')).toBeTruthy()
    expect(within(dlg).queryByText('预览里没有按网站用量')).toBeNull()
  })

  it('switched on but not in the saved list yet (unsaved switch, or no node records it), the preview says why the line is missing', async () => {
    served = { terms, privacy: withUsage(false) }
    mount({ discloseUsage: true })
    const dlg = await openEditor()
    expect(await within(dlg).findByText('预览里没有按网站用量')).toBeTruthy()
    expect(within(dlg).queryByText('按网站用量')).toBeNull()
  })

  it('no hint while the switch is off', async () => {
    served = { terms, privacy: withUsage(false) }
    mount({ discloseUsage: false })
    const dlg = await openEditor()
    expect(within(dlg).getByRole('heading', { name: '隐私' })).toBeTruthy()
    expect(within(dlg).queryByText('预览里没有按网站用量')).toBeNull()
  })
})

describe('publishing (S21)', () => {
  async function toPublish(text = `${privacyText}新增`) {
    mount()
    const dlg = await openEditor()
    type(text)
    fireEvent.click(within(dlg).getByRole('button', { name: '发布…' }))
    return screen.findByRole('dialog', { name: '发布隐私政策（zh-CN）第 4 版？' })
  }

  it('names the document, locale and next version, and how much the text changed', async () => {
    const pub = await toPublish()
    expect(within(pub).getByText('比上一版多 2 字、少 0 字')).toBeTruthy()
    expect((within(pub).getByRole('checkbox', { name: '重大变更：要求用户重新同意' }) as HTMLInputElement).checked).toBe(false)
  })

  it('a material change says how many ordinary users will be asked', async () => {
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('checkbox', { name: '重大变更：要求用户重新同意' }))
    expect(await within(pub).findByText(/约 1,240 名普通用户/)).toBeTruthy()
    // Users are asked when they next open the portal, not at a sign-in (a session can be days old).
    expect(within(pub).getByText(/下次打开门户时/)).toBeTruthy()
    expect(within(pub).queryByText(/下次登录时/)).toBeNull()
  })

  it('publishes the text with the box as asked, says so, and closes both dialogs', async () => {
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('checkbox', { name: '重大变更：要求用户重新同意' }))
    fireEvent.click(within(pub).getByRole('button', { name: '发布' }))
    await waitFor(() => expect(legalApi.publishLegal).toHaveBeenCalledWith('privacy', { locale: 'zh-CN', content: `${privacyText}新增`, consent_bump: true }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已发布第 4 版', 'success'))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('an unmarked publication sends consent_bump false', async () => {
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: '发布' }))
    await waitFor(() => expect(legalApi.publishLegal).toHaveBeenCalledWith('privacy', expect.objectContaining({ consent_bump: false })))
  })

  it('a version conflict says another administrator published, reloads, and keeps the draft', async () => {
    vi.mocked(legalApi.publishLegal).mockRejectedValue(Object.assign(new Error('409'), { response: { status: 409, data: {} } }))
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: '发布' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('其他管理员刚发布了新版本，已重新载入', 'warning'))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /发布隐私政策/ })).toBeNull())
    expect(code().value).toBe(`${privacyText}新增`)
    expect(vi.mocked(legalApi.getAdminLegal).mock.calls.length).toBeGreaterThan(2)
  })

  it('any other failure is said in the dialog, which stays', async () => {
    vi.mocked(legalApi.publishLegal).mockRejectedValue(new Error('boom'))
    const pub = await toPublish()
    fireEvent.click(within(pub).getByRole('button', { name: '发布' }))
    expect(await within(pub).findByText('发布失败，请重试')).toBeTruthy()
    expect(screen.getByRole('dialog', { name: /发布隐私政策/ })).toBeTruthy()
  })

  it('in English', async () => {
    setTestLang('en-US')
    mount()
    fireEvent.click(within(await card('Privacy Policy')).getByRole('button', { name: 'Edit' }))
    const dlg = await screen.findByRole('dialog', { name: 'Edit Privacy Policy' })
    type(`${privacyText}x`)
    fireEvent.click(within(dlg).getByRole('button', { name: 'Publish…' }))
    expect(await screen.findByRole('dialog', { name: 'Publish Privacy Policy (zh-CN) version 4?' })).toBeTruthy()
  })
})

describe('the version history', () => {
  it('lists every version, newest first, with the material changes marked', async () => {
    mount()
    fireEvent.click(within(await card('服务条款')).getByRole('button', { name: '版本历史' }))
    const sheet = await screen.findByRole('dialog', { name: '服务条款版本历史' })
    const rows = (await within(sheet).findAllByRole('listitem')).map(li => li.textContent)
    expect(rows).toEqual([
      expect.stringContaining('zh-CN · v4'), expect.stringContaining('en-US · v2'), expect.stringContaining('zh-CN · v3'),
    ])
    expect(rows[0]).toContain('重大变更')
    expect(rows[1]).not.toContain('重大变更')
  })

  it('says when nothing was ever published', async () => {
    served = { terms: nothing('terms'), privacy: nothing('privacy') }
    mount()
    fireEvent.click(within(await card('服务条款')).getByRole('button', { name: '版本历史' }))
    expect(await screen.findByText('还没有发布过')).toBeTruthy()
  })
})
