// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { ThemeProvider } from '@mui/material/styles'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import type { DataCollection, LegalAdminDocument } from '@/api/legal'
import { flatten, type Nested } from '@/i18n/options'
import zh from '@/locales/zh-CN/admin.json'
import authZh from '@/locales/zh-CN/auth.json'

vi.setConfig({ testTimeout: 20_000 })
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
const confirm = vi.hoisted(() => vi.fn())
const snack = vi.hoisted(() => vi.fn())
const copy = vi.hoisted(() => ({ admin: {} as Record<string, string>, auth: {} as Record<string, string> }))
const data = vi.hoisted(() => ({ settings: {} as Record<string, unknown> }))
const previewState = vi.hoisted(() => ({ crash: false }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/ConfirmHost', () => ({ confirm }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack }))
vi.mock('@/i18n', () => ({ SUPPORTED_LANGUAGES: ['zh-CN', 'zh-TW', 'en-US', 'ja-JP'], isBuiltinLanguage: (lang: string) => ['zh-CN', 'zh-TW', 'en-US'].includes(lang), default: { t: (key: string) => key, language: 'zh-CN' } }))
vi.mock('react-i18next', () => ({ useTranslation: (namespace: string) => ({ t: (key: string, values?: Record<string, unknown>) => {
  const dictionary = namespace === 'auth' ? copy.auth : copy.admin
  return (dictionary[key.replace(/^admin:/, '')] ?? key).replace(/\{\{(\w+)\}\}/g, (match, name: string) => values && name in values ? String(values[name]) : match)
}, i18n: { language: 'zh-CN' } }) }))
vi.mock('@/query/useQueryScope', () => ({ useQueryScope: () => ({ role: 'admin', userId: 1 }) }))
vi.mock('@/query/settings', () => ({ useUISettings: () => ({ data: data.settings, refetch: async () => ({ data: data.settings }) }), useMailSettings: vi.fn(), useOidcConfig: vi.fn(), useSamlConfig: vi.fn() }))
vi.mock('@/components/CodeEditor', async () => {
  const { forwardRef, useImperativeHandle } = await import('react')
  return { default: forwardRef(function TestEditor({ value, onChange, ariaLabel, readOnly }: { value: string; onChange: (text: string) => void; ariaLabel: string; readOnly: boolean }, ref) {
    useImperativeHandle(ref, () => ({ insertLine: (text: string) => onChange(value + '\n' + text + '\n') }))
    return <textarea aria-label={ariaLabel} value={value} readOnly={readOnly} onChange={event => onChange(event.target.value)} />
  }) }
})
vi.mock('@/components/LegalDocument', async importOriginal => {
  const original = await importOriginal<typeof import('@/components/LegalDocument')>()
  return { ...original, default: (props: Parameters<typeof original.default>[0]) => {
    if (previewState.crash) throw new Error('preview test failure')
    return <original.default {...props} />
  } }
})
import LegalTab from './LegalTab'
import LegalHistorySheet from './LegalHistorySheet'
import LegalPublishDialog from './LegalPublishDialog'
import SettingsView from '../SettingsView'

copy.admin = flatten(zh as Nested); copy.auth = flatten(authZh as Nested)
const collection: DataCollection = { sub_log_retention_days: 0, auth_event_retention_days: 90, connection_retention_days: 7, hwid_captured: false, hwid_retention_days: 0, flag_record_retention_days: 90, risk_assessment_refresh_minutes: 30, risk_review_purge_after_deletion_minutes: 60, access: [] }
const document: LegalAdminDocument = { id: 6, kind: 'privacy', locale: 'zh-CN', version: 3, content: '上一版\n', consent_bump: false, published_at: '2026-10-09T00:00:00Z', published_by: 1 }
const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })
let latest: LegalAdminDocument | null
let routes: ReturnType<typeof createMemoryRouter>
beforeEach(() => {
  vi.clearAllMocks(); latest = { ...document }; previewState.crash = false
  confirm.mockResolvedValue(false)
  data.settings = { sub_base_url: 'https://panel.example.test', panel_path: '', node_poll_seconds: 30, full_report_seconds: 60, legal_enabled: false, legal_consent_version: 4, quick_links: [], sub_clients: [] }
  api.get.mockImplementation(async (url: string, options?: { params?: { lang?: string } }) => {
    if (url.endsWith('/latest')) {
      if (!latest || options?.params?.lang !== 'zh-CN' || url.includes('/terms/')) throw { response: { status: 404 } }
      return { data: latest }
    }
    if (url.endsWith('/data-collection')) return { data: collection }
    if (url.endsWith('/affected-users')) return { data: { count: 1240 } }
    if (url === '/admin/groups') return { data: { items: [], total: 0 } }
    return { data: {} }
  })
  api.post.mockImplementation(async (_url: string, payload: Record<string, unknown>) => ({ data: { document: { ...document, id: 7, version: 4, ...payload }, consent_version: payload.consent_bump ? 5 : 4 } }))
  api.put.mockImplementation(async (_url: string, payload: Record<string, unknown>) => ({ data: payload }))
})
afterEach(() => { cleanup(); routes?.dispose(); vi.unstubAllGlobals() })

function mount(element = <LegalTab enabled consentVersion={4} onEnabled={vi.fn()} onPublished={vi.fn()} />) {
  routes = createMemoryRouter([{ path: '/admin/settings', element }, { path: '/away', element: <p>已经离开</p> }], { initialEntries: ['/admin/settings?tab=legal'] })
  return render(<ThemeProvider theme={theme}><RouterProvider router={routes} /></ThemeProvider>)
}
async function edit() {
  fireEvent.click((await screen.findAllByRole('button', { name: '编辑' }))[1])
  const field = await screen.findByRole('textbox', { name: '文档正文' })
  await waitFor(() => expect((field as HTMLTextAreaElement).value).toBe(latest?.content ?? ''))
  return field
}

describe('administrator legal documents', () => {
  it('shows fallback and real saved collection, with separate draft switch', async () => {
    const enabled = vi.fn()
    mount(<LegalTab enabled={false} consentVersion={4} onEnabled={enabled} onPublished={vi.fn()} />)
    expect(await screen.findByText('en-US 尚未发布（回落到 zh-CN）')).toBeTruthy()
    expect(screen.getByText('ja-JP 尚未发布（回落到 zh-CN）')).toBeTruthy()
    expect(screen.queryByText(/zh-TW 尚未发布/)).toBeNull()
    expect(await screen.findByText('永久保留')).toBeTruthy()
    fireEvent.click(screen.getByRole('switch', { name: '已启用' }))
    expect(enabled).toHaveBeenCalledWith(true)
    expect(api.put).not.toHaveBeenCalled()
  })
  it('warns when enabled and unpublished, without treating load errors as empty', async () => {
    latest = null; mount(<LegalTab enabled consentVersion={0} onEnabled={vi.fn()} onPublished={vi.fn()} />)
    expect(await screen.findByText(/已启用，但还没有发布任何文档/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '去编辑' }))
    expect(await screen.findByRole('textbox', { name: '文档正文' })).toBeTruthy()
  })
  it('does not claim no documents when a removed language pack still has a publication', async () => {
    latest = null; mount()
    expect(await screen.findAllByText('zh-CN 尚未发布')).toHaveLength(2)
    expect(screen.queryByText(/已启用，但还没有发布任何文档/)).toBeNull()
    expect(screen.getByText('当前同意版本 v4')).toBeTruthy()
  })
  it('retries a failed document read without an unpublished warning', async () => {
    api.get.mockRejectedValueOnce(new Error('offline'))
    mount()
    expect(await screen.findByText('暂时无法加载文档')).toBeTruthy()
    expect(screen.queryByText(/已启用，但还没有发布任何文档/)).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText(/zh-CN v3/)).toBeTruthy()
  })
  it('guards dirty language switches, close and route navigation', async () => {
    mount(); const field = await edit()
    fireEvent.change(field, { target: { value: '未发布草稿' } })
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
    expect(screen.getByRole('textbox', { name: '文档正文' })).toBeTruthy()
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '语言' }))
    fireEvent.click(await screen.findByRole('option', { name: 'en-US' }))
    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(2))
    expect((field as HTMLTextAreaElement).value).toBe('未发布草稿')
    await act(async () => { void routes.navigate('/away') })
    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(3))
    expect(routes.state.location.pathname).toBe('/admin/settings')
    confirm.mockResolvedValueOnce(true)
    await act(async () => { void routes.navigate('/away') })
    expect(await screen.findByText('已经离开')).toBeTruthy()
  })
  it('loads a confirmed language switch through its dedicated latest endpoint', async () => {
    mount(); const field = await edit()
    fireEvent.change(field, { target: { value: '待丢弃' } })
    confirm.mockResolvedValueOnce(true)
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '语言' }))
    fireEvent.click(await screen.findByRole('option', { name: 'ja-JP' }))
    await waitFor(() => expect((screen.getByRole('textbox', { name: '文档正文' }) as HTMLTextAreaElement).value).toBe(''))
    expect(api.get).toHaveBeenCalledWith('/admin/legal/privacy/latest', expect.objectContaining({ params: { lang: 'ja-JP' } }))
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('textbox', { name: '文档正文' })).toBeNull())
    expect(confirm).toHaveBeenCalledTimes(1)
  })
  it('inserts a marker, shares debounced preview and blocks byte overflow', async () => {
    mount(); const field = await edit()
    expect(screen.getByRole('button', { name: /正文里没有/ })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '插入「我们收集什么」' }))
    expect((field as HTMLTextAreaElement).value).toContain('[[data-collection]]')
    expect((screen.getByRole('button', { name: '插入「我们收集什么」' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(field, { target: { value: '# 唯一预览标题\n[[data-collection]]' } })
    expect(screen.queryByRole('heading', { name: '唯一预览标题' })).toBeNull()
    expect(await screen.findByRole('heading', { name: '唯一预览标题' })).toBeTruthy()
    fireEvent.change(field, { target: { value: '中'.repeat(20_001) } })
    expect(screen.getByText('60,003 / 60,000 字节')).toBeTruthy()
    expect((screen.getByRole('button', { name: '发布…' }) as HTMLButtonElement).disabled).toBe(true)
  })
  it('keeps a failed preview inside its column', async () => {
    mount(); const field = await edit()
    const log = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      previewState.crash = true
      fireEvent.change(field, { target: { value: '触发预览异常' } })
      expect(await screen.findByText(/暂时无法渲染预览/)).toBeTruthy()
      expect(screen.getByRole('textbox', { name: '文档正文' })).toBeTruthy()
      expect((screen.getByRole('button', { name: '发布…' }) as HTMLButtonElement).disabled).toBe(false)
    } finally { log.mockRestore() }
  })
  it('uses separate edit and preview panes on a narrow screen', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({ matches: query.includes('max-width'), media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn() }))
    mount(); const field = await edit()
    fireEvent.change(field, { target: { value: '# 窄屏预览' } })
    fireEvent.click(screen.getByRole('tab', { name: '预览' }))
    expect(await screen.findByRole('heading', { name: '窄屏预览' })).toBeTruthy()
    expect(screen.queryByRole('textbox', { name: '文档正文' })).toBeNull()
    expect((screen.getByRole('button', { name: '插入「我们收集什么」' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(screen.getByRole('tab', { name: '编辑' }))
    expect((screen.getByRole('textbox', { name: '文档正文' }) as HTMLTextAreaElement).value).toBe('# 窄屏预览')
  })
  it('publishes separately, reports the committed consent version and preserves other settings drafts', async () => {
    mount(<SettingsView />)
    fireEvent.click(await screen.findByRole('switch', { name: '已启用' }))
    const field = await edit()
    fireEvent.change(field, { target: { value: '新正文' } })
    fireEvent.click(screen.getByRole('button', { name: '发布…' }))
    const publish = await screen.findByRole('dialog', { name: '发布隐私政策（zh-CN）第 4 版？' })
    fireEvent.click(within(publish).getByRole('checkbox', { name: /重大变更/ }))
    expect(await within(publish).findByText('约 1240 名普通用户下次登录时会看到同意对话框。')).toBeTruthy()
    fireEvent.click(within(publish).getByRole('button', { name: '发布' }))
    expect(await screen.findByText('当前同意版本 v5')).toBeTruthy()
    expect(api.post).toHaveBeenCalledWith('/admin/legal/privacy', { locale: 'zh-CN', content: '新正文', consent_bump: true, expected_version: 3 }, { _skipErrorToast: true })
    expect(api.put).not.toHaveBeenCalled()
    expect((screen.getByRole('switch', { name: '已启用' }) as HTMLInputElement).checked).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.put).toHaveBeenCalled())
    expect(api.put.mock.calls[0][1]).toMatchObject({ legal_enabled: true, legal_consent_version: 5 })
  })
  it('keeps the draft after a version conflict and compares the refreshed version next time', async () => {
    mount(); const field = await edit()
    fireEvent.change(field, { target: { value: '保留我的草稿' } })
    api.post.mockImplementationOnce(async () => { latest = { ...document, version: 4, content: '他人新正文' }; throw { response: { status: 409 } } })
    fireEvent.click(screen.getByRole('button', { name: '发布…' }))
    const publish = await screen.findByRole('dialog', { name: /第 4 版/ })
    fireEvent.click(within(publish).getByRole('button', { name: '发布' }))
    expect(await screen.findByText(/其他管理员刚发布了新版本，已重新载入/)).toBeTruthy()
    expect((screen.getByRole('textbox', { name: '文档正文' }) as HTMLTextAreaElement).value).toBe('保留我的草稿')
    fireEvent.click(screen.getByRole('button', { name: '发布…' }))
    expect(await screen.findByRole('dialog', { name: /第 5 版/ })).toBeTruthy()
  })
  it('blocks publication until a failed conflict reload succeeds, preserving the draft', async () => {
    mount(); const field = await edit()
    fireEvent.change(field, { target: { value: '保留我的草稿' } })
    api.post.mockImplementationOnce(async () => { api.get.mockRejectedValueOnce(new Error('reload offline')); latest = { ...document, version: 5 }; throw { response: { status: 409 } } })
    fireEvent.click(screen.getByRole('button', { name: '发布…' }))
    const publish = await screen.findByRole('dialog', { name: /第 4 版/ })
    fireEvent.click(within(publish).getByRole('button', { name: '发布' }))
    expect(await screen.findByText(/暂时无法重新载入/)).toBeTruthy()
    expect((screen.getByRole('button', { name: '发布…' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText(/已重新载入/)).toBeTruthy()
    expect((screen.getByRole('textbox', { name: '文档正文' }) as HTMLTextAreaElement).value).toBe('保留我的草稿')
    fireEvent.click(screen.getByRole('button', { name: '发布…' }))
    expect(await screen.findByRole('dialog', { name: /第 6 版/ })).toBeTruthy()
  })
  it('does not publish a major change with an unknown count; retries with actual zero', async () => {
    api.get.mockRejectedValueOnce(new Error('offline')).mockResolvedValue({ data: { count: 0 } })
    const publish = vi.fn().mockResolvedValue(undefined)
    mount(<LegalPublishDialog kind="terms" locale="zh-CN" version={1} previous="" content="内容" busy={false} onCancel={vi.fn()} onPublish={publish} />)
    fireEvent.click(screen.getByRole('checkbox', { name: /重大变更/ }))
    expect((screen.getByRole('button', { name: '发布' }) as HTMLButtonElement).disabled).toBe(true)
    expect(await screen.findByText(/无法读取受影响人数/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('约 0 名普通用户下次登录时会看到同意对话框。')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '发布' }))
    await waitFor(() => expect(publish).toHaveBeenCalledWith(true))
  })
  it('suppresses duplicate publish clicks while the first write is pending', async () => {
    let done!: () => void
    const publish = vi.fn(() => new Promise<void>(resolve => { done = resolve }))
    mount(<LegalPublishDialog kind="terms" locale="zh-CN" version={1} previous="" content="内容" busy={false} onCancel={vi.fn()} onPublish={publish} />)
    const button = screen.getByRole('button', { name: '发布' })
    fireEvent.click(button); fireEvent.click(button)
    expect(publish).toHaveBeenCalledTimes(1)
    await act(async () => done())
  })
  it('shows an empty history without inventing a version', async () => {
    api.get.mockResolvedValueOnce({ data: { items: [], next_before_id: 0 } })
    mount(<LegalHistorySheet kind="terms" collection={collection} onClose={vi.fn()} />)
    expect(await screen.findByText('还没有发布过')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '加载更多' })).toBeNull()
  })
  it('paginates history by immutable IDs and retries a failed next page', async () => {
    api.get.mockResolvedValueOnce({ data: { items: [document], next_before_id: 6 } }).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ data: { items: [{ ...document, id: 5, locale: 'en-US', version: 2 }], next_before_id: 0 } })
    mount(<LegalHistorySheet kind="privacy" collection={collection} onClose={vi.fn()} />)
    expect(await screen.findByText('zh-CN · v3')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '加载更多' }))
    expect(await screen.findByText('暂时无法加载文档')).toBeTruthy()
    expect(screen.getByText('zh-CN · v3')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('en-US · v2')).toBeTruthy()
    expect(api.get.mock.calls[2][1].params).toEqual({ before_id: 6, limit: 50 })
    expect(screen.queryByRole('button', { name: '加载更多' })).toBeNull()
  })
})
