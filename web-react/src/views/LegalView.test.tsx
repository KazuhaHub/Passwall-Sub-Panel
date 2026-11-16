// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as legalApi from '@/api/legal'
import { renderAccess, setLanguageMock, setTestLang, whereIs } from '@/test/accessControlHarness'
import LegalView from './LegalView'

vi.mock('@/api/legal', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/legal')>()
  return { ...real, getPublicLegal: vi.fn() }
})

const doc = (over: Partial<legalApi.PublicLegalDocument> = {}): legalApi.PublicLegalDocument => ({
  kind: 'privacy', version: 3, consent_version: 3, locale: 'zh-CN', content: '本服务……\n\n[[data-collection]]\n\n结尾',
  published_at: '2026-09-30T08:00:00Z',
  data_collection: { sub_log: { enabled: true, retention_days: 0, includes_ip: true } },
  ...over,
})

const open = (route = '/legal/privacy') => renderAccess(<LegalView />, { route, path: '/legal/:kind', role: 'user' })

beforeEach(() => {
  vi.mocked(legalApi.getPublicLegal).mockReset()
  setLanguageMock.mockReset()
})

describe('the public terms and privacy page (S22)', () => {
  it('shows the title, the version line, the text and the generated list', async () => {
    vi.mocked(legalApi.getPublicLegal).mockResolvedValue(doc())
    open()
    expect(await screen.findByRole('heading', { level: 1, name: '隐私政策' })).toBeTruthy()
    expect(screen.getByText(/第 3 版 · 2026年9月30日发布/)).toBeTruthy()
    expect(screen.getByText('本服务……')).toBeTruthy()
    expect(within(screen.getByRole('region', { name: '我们收集什么' })).getByText('永久保留')).toBeTruthy()
    expect(legalApi.getPublicLegal).toHaveBeenCalledWith('privacy', 'zh-CN', expect.anything())
  })

  it('asks for the language in ?lang= and says when another one was served', async () => {
    vi.mocked(legalApi.getPublicLegal).mockResolvedValue(doc({ locale: 'zh-CN', fallback_from: 'en-US' }))
    open('/legal/privacy?lang=en-US')
    expect(await screen.findByText('此语言暂无译本，显示的是 简体中文 版')).toBeTruthy()
    expect(legalApi.getPublicLegal).toHaveBeenCalledWith('privacy', 'en-US', expect.anything())
  })

  it('says the page is not on when the server has nothing to serve (404)', async () => {
    vi.mocked(legalApi.getPublicLegal).mockResolvedValue(null)
    open()
    expect(await screen.findByText('此页面未启用')).toBeTruthy()
    expect(screen.getByRole('button', { name: '返回' })).toBeTruthy()
  })

  it('a failed read says so and retries on request', async () => {
    vi.mocked(legalApi.getPublicLegal).mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(doc())
    open()
    expect(await screen.findByText('暂时无法载入，请稍后再试')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByRole('heading', { level: 1, name: '隐私政策' })).toBeTruthy()
  })

  it('shows a skeleton while loading', () => {
    vi.mocked(legalApi.getPublicLegal).mockReturnValue(new Promise(() => {}))
    open()
    expect(document.querySelectorAll('.MuiSkeleton-root').length).toBeGreaterThan(0)
  })

  it('links the other document in the footer', async () => {
    vi.mocked(legalApi.getPublicLegal).mockResolvedValue(doc())
    const { router } = open()
    fireEvent.click(await screen.findByRole('link', { name: '服务条款' }))
    await waitFor(() => expect(whereIs(router)).toBe('/legal/terms'))
  })

  it('goes back in history when there is some, else to the login page', async () => {
    vi.mocked(legalApi.getPublicLegal).mockResolvedValue(doc())
    const { router } = open()
    await screen.findByRole('heading', { level: 1 })
    fireEvent.click(screen.getByRole('button', { name: '← 返回' }))
    await waitFor(() => expect(whereIs(router)).toBe('/login'))
  })

  it('switching the language writes ?lang= and re-reads the document', async () => {
    vi.mocked(legalApi.getPublicLegal).mockResolvedValue(doc())
    const { router } = open()
    await screen.findByRole('heading', { level: 1 })
    fireEvent.click(screen.getByRole('button', { name: '语言' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'English' }))
    expect(setLanguageMock).toHaveBeenCalledWith('en-US')
    await waitFor(() => expect(whereIs(router)).toBe('/legal/privacy?lang=en-US'))
    await waitFor(() => expect(legalApi.getPublicLegal).toHaveBeenLastCalledWith('privacy', 'en-US', expect.anything()))
  })

  it('an unknown kind goes to the home route', async () => {
    const { router } = open('/legal/cookies')
    await waitFor(() => expect(whereIs(router)).toBe('/'))
    expect(legalApi.getPublicLegal).not.toHaveBeenCalled()
  })

  it('in English', async () => {
    setTestLang('en-US')
    vi.mocked(legalApi.getPublicLegal).mockResolvedValue(doc({ locale: 'en-US' }))
    open()
    expect(await screen.findByRole('heading', { level: 1, name: 'Privacy Policy' })).toBeTruthy()
    expect(screen.getByText(/Version 3 · published/)).toBeTruthy()
  })
})
