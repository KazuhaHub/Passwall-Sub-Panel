/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import UserLookupTab from './UserLookupTab'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
// The detail has its own tests; here it only has to receive the id.
vi.mock('./UserLookupDetail', () => ({ default: ({ userId }: { userId: number }) => <p>{`detail of ${userId}`}</p> }))
const dict = vi.hoisted(() => ({ current: {} as Record<string, string> }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => {
      const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
      const raw = dict.current[flat] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
      return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
    },
    i18n: { language: 'zh-CN' },
  }),
}))

import zh from '@/locales/zh-CN/admin.json'
import { flatten, type Nested } from '@/i18n/options'
dict.current = flatten(zh as Nested)

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
const alice = { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 1 }

function mount(userId: number | null, onPick = vi.fn()) {
  render(
    <ThemeProvider theme={theme}><UserLookupTab userId={userId} onPick={onPick} /></ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
  return onPick
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/users') return { data: { items: [alice], total: 1, page: 1, page_size: 50 } }
    if (url === '/admin/users/7') return { data: alice }
    throw new Error(`unexpected GET ${url}`)
  })
})
afterEach(cleanup)

describe('UserLookupTab', () => {
  it('asks for a user until one is picked', () => {
    mount(null)
    expect(screen.getByText('按用户名或显示名搜索')).toBeTruthy()
    expect(screen.queryByText(/^detail of/)).toBeNull()
  })

  // Only the prop: that the page reads ?tab=user&id= into it is pinned by
  // RiskCenterView.test, through the router.
  it('shows the detail of the account it is given', () => {
    mount(7)
    expect(screen.getByText('detail of 7')).toBeTruthy()
  })

  it('picking a user hands its id up', async () => {
    const onPick = mount(null)
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '选择用户' }))
    fireEvent.click(await screen.findByRole('option', { name: 'Alice (alice)' }))
    expect(onPick).toHaveBeenCalledWith(7)
  })
})
