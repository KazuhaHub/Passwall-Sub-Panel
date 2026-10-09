/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, Link } from 'react-router'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import AppRouter from '@/router/AppRouter'
import { useAuthStore } from '@/stores/auth'
import zh from '@/locales/zh-CN/admin.json'
import common from '@/locales/zh-CN/common.json'
import { flatten, type Nested } from '@/i18n/options'
import type { AccessControlSettingsView } from '@/api/accessControl'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const confirm = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm }))
const snack = vi.hoisted(() => vi.fn())
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack }))
const dictionary = vi.hoisted(() => ({ current: {} as Record<string, string> }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({
  t: (key: string, opts?: Record<string, unknown>) => (dictionary.current[key] ?? key)
    .replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(opts?.[name] ?? '')),
  i18n: { language: 'zh-CN' },
}) }))
import AccessSettingsDialog from './AccessSettingsDialog'
import { accessControlKeys } from '@/query/keys'
import { sessionScope } from '@/query/session'

dictionary.current = {
  ...Object.fromEntries(Object.entries(flatten(zh as Nested)).map(([k, v]) => [`admin:${k}`, v])),
  ...Object.fromEntries(Object.entries(flatten(common as Nested)).map(([k, v]) => [`common:${k}`, v])),
}
const base: AccessControlSettingsView = {
  settings: { dest_hit_retention_days: 0, dest_trial_retention_days: 0, dest_usage_retention_days: 0, dest_list_refresh_hours: 0, dest_policy_apply_min_seconds: 0 },
  defaults: { dest_hit_retention_days: 30, dest_trial_retention_days: 7, dest_usage_retention_days: 7, dest_list_refresh_hours: 24, dest_policy_apply_min_seconds: 60 },
  effective: { dest_hit_retention_days: 30, dest_trial_retention_days: 7, dest_usage_retention_days: 7, dest_list_refresh_hours: 24, dest_policy_apply_min_seconds: 60 },
}
const close = vi.fn()
function mount(focusKey?: 'dest_list_refresh_hours') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const router = createMemoryRouter([
    { path: '/', element: <><Link to="/other">离开页面</Link><AccessSettingsDialog open onClose={close} focusKey={focusKey} /></> },
    { path: '/other', element: <p>其他页面</p> },
  ])
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })}>
    <QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider>
  </ThemeProvider>)
  return { client, router }
}
const field = (name: string) => screen.getByRole('textbox', { name })
const edit = (name: string, value: string) => {
  fireEvent.focus(field(name)); fireEvent.change(field(name), { target: { value } }); fireEvent.blur(field(name))
}
const httpError = (status: number, data: unknown) => ({ isAxiosError: true, message: 'request failed', response: { status, data } })
beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  api.get.mockResolvedValue({ data: structuredClone(base) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...structuredClone(base), settings: { ...base.settings, ...body.settings } } }))
  confirm.mockResolvedValue(true)
})
afterEach(cleanup)

it('keeps the settings title separate from its close button', async () => {
  mount()
  const heading = await screen.findByRole('heading', { name: '数据与下发设置' })
  expect(within(heading).queryByRole('button')).toBeNull()
})

it('admits only one close while its discard confirmation is pending', async () => {
  let answer!: (ok: boolean) => void
  confirm.mockReturnValueOnce(new Promise(resolve => { answer = resolve }))
  mount()
  await screen.findByRole('textbox', { name: '策略下发等待（秒）' })
  edit('策略下发等待（秒）', '120')
  const trigger = screen.getByRole('button', { name: '关闭' })
  fireEvent.click(trigger); fireEvent.click(trigger)
  expect(confirm).toHaveBeenCalledOnce()
  await act(async () => { answer(true) })
  expect(close).toHaveBeenCalledOnce()
  expect(api.put).not.toHaveBeenCalled()
})

it('keeps settings reset and footer actions at least 44px in both dimensions', async () => {
  api.get.mockResolvedValue({ data: { ...structuredClone(base), settings: { ...base.defaults } } })
  mount()
  await screen.findByRole('textbox', { name: '策略下发等待（秒）' })
  for (const button of within(screen.getByRole('dialog')).getAllByRole('button')) {
    const style = getComputedStyle(button)
    expect(Math.max(parseFloat(style.minHeight) || 0, parseFloat(style.height) || 0), button.textContent || button.getAttribute('aria-label') || undefined).toBeGreaterThanOrEqual(44)
    expect(Math.max(parseFloat(style.minWidth) || 0, parseFloat(style.width) || 0), button.textContent || button.getAttribute('aria-label') || undefined).toBeGreaterThanOrEqual(44)
  }
})

it('makes failed-read retry a touch-sized async action without a write or duplicate read', async () => {
  api.get.mockRejectedValueOnce(httpError(500, { error: 'database unavailable' }))
  let finish!: (value: { data: AccessControlSettingsView }) => void
  mount()
  const retry = await screen.findByRole('button', { name: '重试' })
  expect(parseFloat(getComputedStyle(retry).minHeight)).toBeGreaterThanOrEqual(44)
  api.get.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
  fireEvent.click(retry)
  await waitFor(() => expect(retry.getAttribute('aria-busy')).toBe('true'))
  fireEvent.click(retry)
  expect(api.get).toHaveBeenCalledTimes(2)
  expect(api.put).not.toHaveBeenCalled()
  finish({ data: structuredClone(base) })
  await screen.findByRole('textbox', { name: '策略下发等待（秒）' })
})

it('displays zero as unset, uses served defaults, and focuses the requested field', async () => {
  mount('dest_list_refresh_hours')
  await screen.findByRole('textbox', { name: '远程与分类列表刷新间隔（小时）' })
  expect((field('命中记录保留（天）') as HTMLInputElement).value).toBe('')
  expect(field('命中记录保留（天）').getAttribute('placeholder')).toBe('30')
  await waitFor(() => expect(document.activeElement).toBe(field('远程与分类列表刷新间隔（小时）')))
  expect((screen.getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
})
it('saves only the changed key and invalidates scoped settings and status', async () => {
  const { client } = mount()
  await screen.findByRole('textbox', { name: '策略下发等待（秒）' })
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  edit('策略下发等待（秒）', '120')
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/dest/settings', { settings: { dest_policy_apply_min_seconds: 120 } }, { _skipErrorToast: true }))
  expect(confirm).not.toHaveBeenCalled()
  await waitFor(() => expect(snack).toHaveBeenCalledWith('已保存', 'success'))
  const scope = sessionScope({ userId: 42, role: 'admin', authEpoch: 5 })
  expect(invalidate).toHaveBeenCalledWith({ queryKey: accessControlKeys.settings(scope) })
  expect(invalidate).toHaveBeenCalledWith({ queryKey: accessControlKeys.status(scope) })
  expect(close).not.toHaveBeenCalled()
})
it('confirms shortening once, preserves the draft on cancellation, and sends no write', async () => {
  confirm.mockResolvedValue(false)
  mount()
  await screen.findByRole('textbox', { name: '命中记录保留（天）' })
  edit('命中记录保留（天）', '10')
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(confirm).toHaveBeenCalledWith({ title: '缩短保留期？', message: '超过 10 天的记录会在一小时内删除，无法恢复。', confirmText: '缩短', destructive: true }))
  expect(api.put).not.toHaveBeenCalled()
  expect((field('命中记录保留（天）') as HTMLInputElement).value).toBe('10')
})
it.each(['-1', '1.5', 'abc', '366'])('blocks invalid numeric input %s rather than silently resetting or truncating it', async value => {
  mount()
  await screen.findByRole('textbox', { name: '命中记录保留（天）' })
  edit('命中记录保留（天）', value)
  expect(field('命中记录保留（天）').getAttribute('aria-invalid')).toBe('true')
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  expect(api.put).not.toHaveBeenCalled()
  expect(confirm).not.toHaveBeenCalled()
})
it('marks both retention fields when the effective trial retention exceeds hit retention', async () => {
  mount()
  await screen.findByRole('textbox', { name: '命中记录保留（天）' })
  edit('命中记录保留（天）', '5')
  expect(field('命中记录保留（天）').getAttribute('aria-invalid')).toBe('true')
  expect(field('试运行记录保留（天）').getAttribute('aria-invalid')).toBe('true')
  expect((screen.getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
})
it('maps server errors to numeric fields and clears them only when that field is edited', async () => {
  api.put.mockRejectedValue(httpError(400, { errors: { dest_list_refresh_hours: 'changed concurrently' } }))
  mount()
  await screen.findByRole('textbox', { name: '远程与分类列表刷新间隔（小时）' })
  edit('远程与分类列表刷新间隔（小时）', '48')
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  await screen.findByText('changed concurrently')
  expect(field('远程与分类列表刷新间隔（小时）').getAttribute('aria-invalid')).toBe('true')
  edit('策略下发等待（秒）', '120')
  expect(screen.getByText('changed concurrently')).toBeTruthy()
  edit('远程与分类列表刷新间隔（小时）', '72')
  expect(screen.queryByText('changed concurrently')).toBeNull()
})
it('retries failed reads and handles 503 as an unwired information state', async () => {
  api.get.mockRejectedValueOnce(httpError(500, { error: 'database unavailable' }))
  mount()
  await screen.findByText('读取失败：database unavailable')
  fireEvent.click(screen.getByRole('button', { name: '重试' }))
  await screen.findByRole('textbox', { name: '命中记录保留（天）' })
})
it('shows unwired settings without fields or a save action', async () => {
  api.get.mockRejectedValue(httpError(503, { error: 'not wired' }))
  mount()
  await screen.findByText('设置未接线')
  expect(screen.queryByRole('textbox')).toBeNull()
  expect(screen.queryByRole('button', { name: '保存' })).toBeNull()
})
it('makes no private read for an operator', async () => {
  useAuthStore.setState({ role: 'operator' })
  mount()
  await screen.findByText('仅管理员可以修改访问控制设置。')
  expect(api.get).not.toHaveBeenCalled()
})
it('guards both dirty dialog close and route navigation and keeps edits after cancel', async () => {
  confirm.mockResolvedValue(false)
  const { router } = mount()
  await screen.findByRole('textbox', { name: '策略下发等待（秒）' })
  edit('策略下发等待（秒）', '120')
  fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape', code: 'Escape', keyCode: 27 })
  await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
  expect(close).not.toHaveBeenCalled()
  fireEvent.click(screen.getByText('离开页面'))
  await waitFor(() => expect(confirm).toHaveBeenCalledTimes(2))
  expect(router.state.location.pathname).toBe('/')
  expect((field('策略下发等待（秒）') as HTMLInputElement).value).toBe('120')
})

it('holds navigation while shortening confirmation is pending without replacing that confirmation', async () => {
  let answer!: (ok: boolean) => void
  let saved!: (response: { data: AccessControlSettingsView }) => void
  api.put.mockReturnValueOnce(new Promise(resolve => { saved = resolve }))
  confirm.mockReturnValueOnce(new Promise<boolean>(resolve => { answer = resolve }))
  const { router } = mount()
  await screen.findByRole('textbox', { name: '命中记录保留（天）' })
  edit('命中记录保留（天）', '10')
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
  fireEvent.click(screen.getByText('离开页面'))
  await waitFor(() => expect([...router.state.blockers.values()].some(blocker => blocker.state === 'blocked')).toBe(true))
  expect(confirm).toHaveBeenCalledTimes(1)
  expect(router.state.location.pathname).toBe('/')
  await act(async () => { answer(true) })
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(router.state.location.pathname).toBe('/')
  expect(confirm).toHaveBeenCalledTimes(1)
  await act(async () => { saved({ data: { ...base, settings: { ...base.settings, dest_hit_retention_days: 10 } } }) })
  await screen.findByText('其他页面')
  expect(api.put).toHaveBeenCalledTimes(1)
  expect(confirm).toHaveBeenCalledTimes(1)
})

it('blocks close and repeated writes while saving and preserves the draft on transport failure', async () => {
  let fail!: (error: unknown) => void
  api.put.mockReturnValueOnce(new Promise((_, reject) => { fail = reject }))
  mount()
  await screen.findByRole('textbox', { name: '策略下发等待（秒）' })
  edit('策略下发等待（秒）', '120')
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(screen.getByRole('button', { name: '保存中…' }).getAttribute('aria-busy')).toBe('true')
  fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape', code: 'Escape', keyCode: 27 })
  fireEvent.click(screen.getByRole('button', { name: '保存中…' }))
  expect(close).not.toHaveBeenCalled()
  expect(api.put).toHaveBeenCalledTimes(1)
  await act(async () => { fail(httpError(500, { error: 'temporary failure' })) })
  await waitFor(() => expect(snack).toHaveBeenCalledWith('保存失败：temporary failure', 'error'))
  expect((field('策略下发等待（秒）') as HTMLInputElement).value).toBe('120')
})
