// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { Dialog, ThemeProvider } from '@mui/material'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { renderAccess } from '@/test/accessControlHarness'
import { createAppTheme } from '@/theme'
import ListEntriesSheet from './ListEntriesSheet'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const budget = { rules: { used: 0, limit: 1 }, domains: { used: 0, limit: 1 }, regexps: { used: 0, limit: 1 }, cidrs: { used: 0, limit: 1 }, source_cidrs: { used: 0, limit: 1 }, subjects: { used: 0, limit: 1 }, bytes: { used: 0, limit: 1 } }
const list = (o: Partial<api.DestList> = {}): api.DestList => ({
  id: 2, name: 'Scam list', kind: 'remote', side: 'dest', source_url: 'https://raw.example.org/l.txt', entry_count: 8012, regexp_count: 3, state: 'ready',
  empty: false, last_fetched_at: 1, last_error: '', owner_group_id: 0, updated_at: 1, used_by: [], ...o,
})
const onClose = vi.fn(); const onEdit = vi.fn(); const onTest = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestList).mockResolvedValue({ list: list(), samples: ['example-scam.com', 'login.fake-bank.net', 'x.test'] })
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
})

describe('the entries sheet', () => {
  it('shows the figures and the first 200, searches only those in component state, and tests an entry', async () => {
    const { router } = renderAccess(<ListEntriesSheet listId={2} onClose={onClose} onEdit={onEdit} onTest={onTest} />)
    expect(await screen.findByText('Scam list')).toBeTruthy()
    expect(screen.getByText('条目 8,012 · 正则 3')).toBeTruthy()
    expect(screen.getByText('共 8,012 条，这里只显示前 200 条（搜索只在这 200 条里进行）')).toBeTruthy()
    fireEvent.change(screen.getByLabelText('搜索条目'), { target: { value: 'fake' } })
    expect(screen.queryByText('example-scam.com')).toBeNull()
    // The search term never reaches the URL (R12).
    expect(router.state.location.search).toBe('')
    fireEvent.click(screen.getByRole('button', { name: '测试此目的地' }))
    expect(onTest).toHaveBeenCalledWith('login.fake-bank.net')
    fireEvent.click(screen.getByRole('button', { name: '编辑' }))
    expect(onEdit).toHaveBeenCalledWith(2)
  })

  it('offers refresh for a remote list only and starts it', async () => {
    vi.mocked(api.refreshDestList).mockResolvedValue()
    renderAccess(<ListEntriesSheet listId={2} onClose={onClose} onEdit={onEdit} onTest={onTest} />)
    fireEvent.click(await screen.findByRole('button', { name: '立即刷新' }))
    await waitFor(() => expect(api.refreshDestList).toHaveBeenCalledWith(2))
  })

  it('a geosite list has neither edit nor refresh', async () => {
    vi.mocked(api.getDestList).mockResolvedValue({ list: list({ kind: 'geosite', geosite_category: 'cryptocurrency' }), samples: [] })
    renderAccess(<ListEntriesSheet listId={2} onClose={onClose} onEdit={onEdit} onTest={onTest} />)
    expect(await screen.findByText('没有条目')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '编辑' })).toBeNull()
    expect(screen.queryByRole('button', { name: '立即刷新' })).toBeNull()
  })

  it('is closed with no list and asks nothing', () => {
    renderAccess(<ListEntriesSheet listId={null} onClose={onClose} onEdit={onEdit} onTest={onTest} />)
    expect(api.getDestList).not.toHaveBeenCalled()
  })

  // A dialog opened from this drawer (the edit dialog, the test sheet) must sit above it.
  it('keeps the default drawer layer, below a modal opened from it', async () => {
    const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })
    renderAccess(<ThemeProvider theme={theme}><ListEntriesSheet listId={2} onClose={onClose} onEdit={onEdit} onTest={onTest} /><Dialog open><p>d</p></Dialog></ThemeProvider>)
    await screen.findByText('Scam list')
    const z = (el: Element | null) => Number(getComputedStyle(el as Element).zIndex)
    const drawer = document.querySelector('.MuiDrawer-root')
    const modal = document.querySelector('.MuiDialog-root')
    expect(z(modal)).toBeGreaterThan(z(drawer))
    expect(z(drawer)).toBe(theme.zIndex.drawer)
    void render
  })
})
