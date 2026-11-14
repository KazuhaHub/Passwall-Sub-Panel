// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, useLocation } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import { SCOPE_KEYS } from '@/components/scope/scopeOverrides'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import GroupsView from './GroupsView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'zh-CN' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function Where() {
  const l = useLocation()
  return <p data-testid="where">{l.pathname + l.search}</p>
}

function mount(route = '/') {
  render(
    <MemoryRouter initialEntries={[route]}>
      <ThemeProvider theme={theme}>
        <GroupsView />
        <Where />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('GroupsView', () => {
  it('reports a failed read instead of showing an empty group list', async () => {
    // The mount load had no catch, so a failure raised an unhandled rejection
    // and the table rendered empty — indistinguishable from "no groups exist".
    api.get.mockRejectedValue(new Error('offline'))
    mount()

    await waitFor(() => expect(screen.getByText('暂时无法加载分组')).toBeTruthy())
  })

  // The detector exceptions (locations, auto-suspension, risk signals) are
  // edited on the risk center's policy page, beside the global values they
  // override; the dialog offers none of them, only the way there. In a new
  // tab: the dialog tracks no unsaved state, so navigating this tab away
  // would drop the admin's group edits without a word.
  it('leaves the detector exceptions to the risk center, linked in a new tab', async () => {
    useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
    const group = { id: 5, slug: 'team', name: 'Team', tag_filter: { all: true, tags: [], mode: 'all' }, members: 0, remark: '', require_2fa: false }
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/groups') return { data: { items: [group], total: 1, page: 1, page_size: 25 } }
      if (url === '/admin/nodes') return { data: { items: [], total: 0, page: 1, page_size: 500 } }
      if (url === '/admin/groups/5/scope-settings') {
        return { data: { overrides: {}, overridable: SCOPE_KEYS.map(k => k.key) } }
      }
      if (url === '/admin/settings/ui') return { data: {} }
      throw new Error(`Unexpected GET ${url}`)
    })
    mount()
    fireEvent.click(await screen.findByRole('button', { name: 'admin:groups.action.edit' }))
    fireEvent.click(await screen.findByRole('tab', { name: '策略' }))

    // The categories this dialog still owns render; the three detector ones,
    // and every one of their rows, do not.
    expect(await screen.findByText('通知阈值')).toBeTruthy()
    for (const text of ['异地并发检测', '异地并发 · 自动临时暂停', '风险信号（只提示）', '国家容错', '启用自动临时暂停', '常驻天数']) {
      expect(screen.queryByText(text), text).toBeNull()
    }

    expect(screen.getByText('admin:groups.scope.policy_pointer')).toBeTruthy()
    const link = screen.getByRole('link', { name: 'admin:groups.scope.open_policy' })
    expect(link.getAttribute('href')).toBe('/admin/risk?tab=policy&group=5')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
  })

  // THE DESTINATIONS TAB AND THE LIST BADGE (S17): the allowlist is edited on the
  // access-control page; here it is a summary, a way there, and a badge on the row.
  describe('the allowlist summary', () => {
    const teamGroup = (over: Record<string, unknown> = {}) => ({
      id: 5, slug: 'team', name: 'Team', tag_filter: { all: true, tags: [], mode: 'all' }, members: 3, remark: '', require_2fa: false, ...over,
    })
    const destGroup = { group_id: 5, group_name: 'Team', members: 3, state: 'on', state_changed_at: 1, updated_at: 1, list_ids: [], lists: [], base_list_id: 0, extra_list_id: 0, base_count: 0, eligible_nodes: 1, total_nodes: 1, ineligible: [], applied_nodes: 1, pending_nodes: [], not_ready_lists: [], would_deny_recent: null, denied_24h: 0 }
    function serve(groups: unknown[]) {
      api.get.mockImplementation(async (url: string) => {
        if (url === '/admin/groups') return { data: { items: groups, total: groups.length, page: 1, page_size: 25 } }
        if (url === '/admin/nodes') return { data: { items: [], total: 0, page: 1, page_size: 500 } }
        if (url === '/admin/dest/groups') return { data: { items: [destGroup] } }
        if (url.startsWith('/admin/groups/') && url.endsWith('/scope-settings')) return { data: { overrides: {}, overridable: SCOPE_KEYS.map(k => k.key) } }
        if (url === '/admin/settings/ui') return { data: {} }
        throw new Error(`Unexpected GET ${url}`)
      })
    }

    it('shows an administrator the summary and the way to the access-control page in a destinations tab', async () => {
      useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
      serve([teamGroup({ dest_state: 'on' })])
      mount()
      fireEvent.click(await screen.findByRole('button', { name: 'admin:groups.action.edit' }))
      fireEvent.click(await screen.findByRole('tab', { name: '目的地' }))
      expect(await screen.findByText('admin:groups.destination.on')).toBeTruthy()
      expect(screen.getByRole('button', { name: 'admin:groups.destination.edit' })).toBeTruthy()
    })

    it('a group that is not saved yet only says to save it first', async () => {
      useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
      serve([])
      mount()
      fireEvent.click(await screen.findByRole('button', { name: 'admin:groups.create' }))
      fireEvent.click(await screen.findByRole('tab', { name: '目的地' }))
      expect(await screen.findByText('admin:groups.destination.save_first')).toBeTruthy()
    })

    it('an operator sees the badge on the row, and no way to edit', async () => {
      useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
      serve([teamGroup({ dest_state: 'report' }), teamGroup({ id: 6, slug: 'other', name: 'Other', dest_state: 'off' })])
      mount()
      expect(await screen.findByText('admin:groups.destination.badge_report')).toBeTruthy()
      expect(screen.queryByText('admin:groups.destination.badge_on')).toBeNull()
      expect(screen.getAllByTestId('allowlist-state')).toHaveLength(1)
      expect(screen.queryByRole('button', { name: 'admin:groups.action.edit' })).toBeNull()
    })

    it('a deep link opens the group on the destinations tab, once, and drops the two params', async () => {
      useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
      serve([teamGroup({ dest_state: 'on' })])
      mount('/?edit=5&dtab=destination')
      expect(await screen.findByText('admin:groups.destination.on', {}, { timeout: 3000 })).toBeTruthy()
      expect(screen.getByRole('tab', { name: '目的地' }).getAttribute('aria-selected')).toBe('true')
      await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('/'))
    })
  })
})
