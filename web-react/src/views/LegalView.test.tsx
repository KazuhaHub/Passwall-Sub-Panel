// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { flatten, type Nested } from '@/i18n/options'
import zh from '@/locales/zh-CN/auth.json'
import type { LegalPublicDocument } from '@/api/legal'
import AppRouter from '@/router/AppRouter'
import LegalView from './LegalView'

const mocks = vi.hoisted(() => ({ get: vi.fn(), language: vi.fn(), loadSite: vi.fn(), dictionary: {} as Record<string, string> }))
vi.mock('@/api/legal', () => ({ getLegalDocument: mocks.get }))
vi.mock('@/i18n', () => ({ currentLanguage: () => 'zh-CN', setLanguage: mocks.language }))
vi.mock('@/stores/site', () => ({ useSiteStore: (selector: (state: { siteTitle: string; load: () => Promise<void> }) => unknown) => selector({ siteTitle: 'Test site', load: mocks.loadSite }) }))
vi.mock('@/components/BrandLogo', () => ({ default: () => <span>Test logo</span> }))
vi.mock('@/components/LanguageMenu', () => ({ default: ({ onChange }: { onChange: (lang: string) => void }) => <button onClick={() => onChange('en-US')}>English</button> }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({
  t: (key: string, values?: Record<string, unknown>) => (mocks.dictionary[key] ?? key).replace(/\{\{(\w+)\}\}/g, (match, name: string) => values && name in values ? String(values[name]) : match),
  i18n: { language: 'zh-CN' },
}) }))
mocks.dictionary = flatten(zh as Nested)

const document: LegalPublicDocument = {
  version: 2, consent_version: 5, locale: 'zh-CN', fallback_from: 'zh-TW', content: 'Actual policy', published_at: '2026-10-09T00:00:00Z',
  data_collection: { sub_log_retention_days: 0, auth_event_retention_days: 0, connection_retention_days: 7, hwid_captured: false,
    hwid_retention_days: 0, flag_record_retention_days: 90, risk_assessment_refresh_minutes: 60, risk_review_purge_after_deletion_minutes: 60, access: [] },
}

function mount(path = '/legal/privacy?lang=zh-TW') {
  const router = createMemoryRouter([{ path: '/legal/:kind', element: <LegalView /> }, { path: '/login', element: <p>Login page</p> }], { initialEntries: [path] })
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })}><AppRouter router={router} /></ThemeProvider>)
  return router
}

beforeEach(() => { vi.clearAllMocks(); mocks.loadSite.mockResolvedValue(undefined) })
afterEach(cleanup)

describe('LegalView', () => {
  it('loads anonymously, shows fallback and switches language in the URL', async () => {
    mocks.get.mockResolvedValue(document)
    const router = mount()
    expect(await screen.findByText('Actual policy')).toBeTruthy()
    expect(screen.getByText('此语言暂无译本，显示的是 zh-CN 版')).toBeTruthy()
    expect(mocks.get.mock.calls[0][0]).toBe('privacy')
    expect(mocks.get.mock.calls[0][1]).toBe('zh-TW')
    fireEvent.click(screen.getByRole('button', { name: 'English' }))
    await waitFor(() => expect(router.state.location.search).toBe('?lang=en-US'))
    expect(mocks.language).toHaveBeenCalledWith('en-US')
    await waitFor(() => expect(mocks.get.mock.calls.at(-1)?.[1]).toBe('en-US'))
    expect(screen.getByRole('link', { name: '服务条款' }).getAttribute('href')).toBe('/legal/terms?lang=en-US')
  })

  it('shows disabled state and returns to login without prior app history', async () => {
    mocks.get.mockRejectedValue({ response: { status: 404 } })
    mount()
    expect(await screen.findByRole('heading', { name: '此页面未启用' })).toBeTruthy()
    expect(screen.queryByText('Actual policy')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '返回' }))
    expect(await screen.findByText('Login page')).toBeTruthy()
  })

  it('renders a read error with a working retry', async () => {
    mocks.get.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(document)
    mount()
    expect(await screen.findByText('暂时无法载入，请稍后再试')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('Actual policy')).toBeTruthy()
    expect(mocks.get).toHaveBeenCalledTimes(2)
  })

  it('ignores the stale response after a language change', async () => {
    let resolveOld!: (doc: LegalPublicDocument) => void
    mocks.get.mockImplementationOnce(() => new Promise<LegalPublicDocument>(resolve => { resolveOld = resolve })).mockResolvedValueOnce({ ...document, content: 'New language', locale: 'en-US' })
    mount()
    expect(screen.getByRole('status', { name: '正在加载文档' })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'English' }))
    expect(await screen.findByText('New language')).toBeTruthy()
    await act(async () => { resolveOld(document) })
    expect(screen.queryByText('Actual policy')).toBeNull()
    expect(screen.getByText('New language')).toBeTruthy()
  })
})
