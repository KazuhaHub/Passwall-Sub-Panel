/** @vitest-environment jsdom */
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { ThemeProvider } from '@mui/material'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { flatten, I18N_STATIC_OPTIONS } from '@/i18n/options'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { destinationNode, destinationStatus } from '@/test/accessControlFixtures'
import type { DestinationNodeStatus } from '@/api/accessControl'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import zhCommon from '@/locales/zh-CN/common.json'
import enCommon from '@/locales/en-US/common.json'
import AuditCollectDialog from './AuditCollectDialog'
import NodeCoverageDrawer from './NodeCoverageDrawer'
import ServerAccessDialog from '../ServerAccessDialog'
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
const snack = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack }))
const effective = { dest_hit_retention_days: 43, dest_usage_retention_days: 9 }
const values: Record<string, unknown> = { '/admin/dest/settings': { settings: { dest_hit_retention_days: 0 }, effective },
  '/admin/settings/ui': { node_poll_seconds: 125, legal_enabled: false } }
beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  api.get.mockImplementation(async (url: string) => ({ data: values[url] }))
  api.put.mockResolvedValue({ data: {} })
})
afterEach(cleanup)
async function mount(overrides: Partial<DestinationNodeStatus> = {}, lang = 'zh-CN', onClose = vi.fn(), entry?: 'coverage' | 'server') {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({ ...I18N_STATIC_OPTIONS, lng: lang, resources: {
    'zh-CN': { admin: flatten(zh), common: flatten(zhCommon) }, 'en-US': { admin: flatten(en), common: flatten(enCommon) },
  } })
  const client = makeTestQueryClient()
  const node = destinationNode({ supports: { policy: true, hits: true, usage: true }, ...overrides })
  const status = destinationStatus({ nodes: [node] })
  const page = entry === 'coverage'
    ? <NodeCoverageDrawer status={status} loading={false} failed={false} refreshing={false} onRetryRead={vi.fn()} onClose={onClose} onLists={vi.fn()} onSettings={vi.fn()} />
    : entry === 'server' ? <ServerAccessDialog server={{ id: 1, name: 'Tokyo', panel_type: 'psp', capabilities: [], url: '', has_api_token: false, has_password: false, auth_method: '', insecure_https: false }} onClose={onClose} />
      : <AuditCollectDialog node={node} onClose={onClose} />
  render(<I18nextProvider i18n={i18n}><MemoryRouter><ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: lang })}>
    <QueryClientProvider client={client}>{page}</QueryClientProvider>
  </ThemeProvider></MemoryRouter></I18nextProvider>)
  return { client, onClose, copy: (lang === 'zh-CN' ? zh : en).access_control.collect, common: (lang === 'zh-CN' ? zhCommon : enCommon).actions }
}
const enabled = (button: HTMLElement) => expect((button as HTMLButtonElement).disabled).toBe(false)

it.each(['zh-CN', 'en-US'])('uses server effective retention, explains trial privacy and saves only the selected mode in %s', async lang => {
  const { client, onClose, copy, common } = await mount({}, lang)
  await screen.findByRole('radio', { name: copy.hits })
  expect(screen.getByText(copy.hits_detail.replace('{{hitDays}}', '43'))).toBeTruthy()
  expect(screen.getByText(copy.disclosure_hits)).toBeTruthy()
  fireEvent.click(screen.getByText(copy.hits_and_usage))
  expect(screen.getByText(copy.hits_and_usage_detail.replace('{{usageDays}}', '9'))).toBeTruthy()
  expect(screen.getByText(copy.disclosure_usage)).toBeTruthy()
  expect(screen.getByRole('button', { name: copy.legal_disabled })).toBeTruthy()
  expect(screen.getByRole('link', { name: copy.open_legal }).getAttribute('href')).toBe('/admin/settings?tab=legal')
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  enabled(screen.getByRole('button', { name: common.save }))
  fireEvent.click(screen.getByRole('button', { name: common.save }))
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(api.put).toHaveBeenCalledExactlyOnceWith('/admin/servers/1', { audit_collect: 'hits_and_usage' })
  expect(snack).toHaveBeenCalledWith(copy.saved.replace('{{node}}', 'Tokyo').replace('{{minutes}}', '3'), 'success')
  expect(invalidate).toHaveBeenCalledWith(expect.objectContaining({ queryKey: expect.arrayContaining(['servers']) }))
  expect(invalidate).toHaveBeenCalledWith(expect.objectContaining({ queryKey: expect.arrayContaining(['status']) }))
})

it.each([
  { legal: undefined, warning: 'legal_unknown' as const },
  { legal: true, warning: null },
])('distinguishes unknown and enabled legal state without blocking usage ($legal)', async ({ legal, warning }) => {
  api.get.mockImplementation(async (url: string) => ({ data: url === '/admin/settings/ui' ? { node_poll_seconds: 0, legal_enabled: legal } : values[url] }))
  const { copy, common } = await mount()
  fireEvent.click(await screen.findByRole('radio', { name: copy.hits_and_usage }))
  if (warning) expect(screen.getByRole('button', { name: copy[warning] })).toBeTruthy()
  else expect(screen.queryByRole('link', { name: copy.open_legal })).toBeNull()
  enabled(screen.getByRole('button', { name: common.save }))
})

it.each([
  { supports: { policy: true, hits: false, usage: false }, hits: true, usage: true },
  { supports: { policy: true, hits: true, usage: false }, hits: false, usage: true },
])('limits choices to reported hit and usage capabilities ($hits/$usage)', async ({ supports, hits, usage }) => {
  const { copy } = await mount({ supports })
  expect((await screen.findByRole('radio', { name: copy.hits }) as HTMLInputElement).disabled).toBe(hits)
  expect((screen.getByRole('radio', { name: copy.hits_and_usage }) as HTMLInputElement).disabled).toBe(usage)
  enabled(screen.getByRole('radio', { name: copy.off }))
})

it.each(['sing-box', null] as const)('keeps %s engine collection read-only even with claimed capabilities', async engine => {
  const { copy, common } = await mount({ engine })
  await screen.findByRole('radio', { name: copy.off })
  for (const radio of screen.getAllByRole('radio')) expect((radio as HTMLInputElement).disabled).toBe(true)
  expect((screen.getByRole('button', { name: common.save }) as HTMLButtonElement).disabled).toBe(true)
  expect(screen.getByText(copy[engine === 'sing-box' ? 'sing_box' : 'engine_unknown'])).toBeTruthy()
  expect(api.put).not.toHaveBeenCalled()
})

it('keeps unknown retention unavailable, retries reads, then permits an explicit off transition', async () => {
  let failed = true
  api.get.mockImplementation(async (url: string) => {
    if (failed && url === '/admin/dest/settings') throw new Error('offline')
    return { data: values[url] }
  })
  const { copy, common, onClose } = await mount()
  await screen.findByText(copy.read_failed)
  expect(screen.queryByRole('radio')).toBeNull()
  expect((screen.getByRole('button', { name: common.save }) as HTMLButtonElement).disabled).toBe(true)
  failed = false
  fireEvent.click(screen.getByRole('button', { name: common.retry }))
  fireEvent.click(await screen.findByRole('radio', { name: copy.off }))
  expect(screen.getByText(copy.off_detail)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: common.save }))
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(api.put).toHaveBeenCalledExactlyOnceWith('/admin/servers/1', { audit_collect: 'off' })
})

it('does not treat a malformed successful settings response as default retention', async () => {
  api.get.mockResolvedValue({ data: '<html>SPA fallback</html>' })
  const { copy, common } = await mount()
  await screen.findByText(copy.read_failed)
  expect(screen.queryByRole('radio')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: common.save }))
  expect(api.put).not.toHaveBeenCalled()
})

it('admits one save, blocks closing and changes in flight, then preserves the draft on failure', async () => {
  let reject!: (error: unknown) => void
  api.put.mockImplementation(() => new Promise((_ok, fail) => { reject = fail }))
  const { copy, common, onClose } = await mount()
  fireEvent.click(await screen.findByRole('radio', { name: copy.off }))
  const button = screen.getByRole('button', { name: common.save })
  fireEvent.click(button); fireEvent.click(button)
  await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
  expect((screen.getByRole('button', { name: common.cancel }) as HTMLButtonElement).disabled).toBe(true)
  expect((screen.getByRole('radio', { name: copy.hits }) as HTMLInputElement).disabled).toBe(true)
  await act(async () => reject(new Error('Could not save')))
  await screen.findByText('Could not save')
  expect((screen.getByRole('radio', { name: copy.off }) as HTMLInputElement).checked).toBe(true)
  expect(onClose).not.toHaveBeenCalled()
  enabled(button)
})

it('drops an in-flight dialog on permission loss without a late success notice or close callback', async () => {
  let finish!: (data: unknown) => void
  api.put.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  const { copy, common, onClose } = await mount()
  fireEvent.click(await screen.findByRole('radio', { name: copy.off }))
  fireEvent.click(screen.getByRole('button', { name: common.save }))
  await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
  act(() => useAuthStore.setState({ role: 'operator', authEpoch: 6 }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  await act(async () => finish({ data: {} }))
  expect(onClose).not.toHaveBeenCalled()
  expect(snack).not.toHaveBeenCalled()
})

it('does not read or write collection controls for a non-native node', async () => {
  await mount({ kind: '3xui' })
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(api.get).not.toHaveBeenCalled()
  expect(api.put).not.toHaveBeenCalled()
})

it.each(['coverage', 'server'] as const)('opens the shared collection dialog from the actual %s entry and saves one off transition', async entry => {
  const node = destinationNode({ supports: { policy: true, hits: true, usage: true } })
  api.get.mockImplementation(async (url: string) => ({ data: url === '/admin/dest/status' ? destinationStatus({ nodes: [node] }) : values[url] }))
  const { copy, common } = await mount({}, 'zh-CN', vi.fn(), entry)
  fireEvent.click(await screen.findByRole('button', { name: zh.access_control.coverage.more.replace('{{name}}', 'Tokyo') }))
  fireEvent.click(await screen.findByRole('menuitem', { name: copy.open }))
  await screen.findByRole('dialog', { name: copy.title.replace('{{node}}', 'Tokyo') })
  fireEvent.click(await screen.findByRole('radio', { name: copy.off }))
  fireEvent.click(screen.getByRole('button', { name: common.save }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: copy.title.replace('{{node}}', 'Tokyo') })).toBeNull())
  expect(api.put).toHaveBeenCalledExactlyOnceWith('/admin/servers/1', { audit_collect: 'off' })
})

it('waits for settings without exposing guessed retention or sending a write', async () => {
  let finish!: (data: unknown) => void
  api.get.mockImplementation((url: string) => url === '/admin/dest/settings' ? new Promise(resolve => { finish = resolve }) : Promise.resolve({ data: values[url] }))
  const { copy, common } = await mount()
  expect(screen.getByRole('progressbar', { name: copy.loading })).toBeTruthy()
  expect(screen.queryByRole('radio')).toBeNull()
  expect(api.put).not.toHaveBeenCalled()
  await act(async () => finish({ data: values['/admin/dest/settings'] }))
  await screen.findByRole('radio', { name: copy.hits })
  expect((screen.getByRole('button', { name: common.save }) as HTMLButtonElement).disabled).toBe(true)
})

it('discards an unsaved selection on an administrator identity change without leaking it into the next session', async () => {
  const { copy, common } = await mount()
  fireEvent.click(await screen.findByRole('radio', { name: copy.off }))
  act(() => useAuthStore.setState({ userId: 77, authEpoch: 6 }))
  await waitFor(() => expect((screen.getByRole('radio', { name: copy.hits }) as HTMLInputElement).checked).toBe(true))
  expect((screen.getByRole('button', { name: common.save }) as HTMLButtonElement).disabled).toBe(true)
  expect(api.put).not.toHaveBeenCalled()
})

it('does not read controls for an operator even if mounted directly', async () => {
  useAuthStore.setState({ role: 'operator' })
  await mount()
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(api.get).not.toHaveBeenCalled()
})

it('shows retained node hits without claiming current collection and links to that exact node', async () => {
  const node = destinationNode({ supports: { policy: true, hits: true, usage: true }, collect: 'off', collecting: false,
    hits_24h: 71, losses: { rows: 3, events: 5, unmatched: 7, scope: 'panel', complete: false } })
  api.get.mockImplementation(async (url: string) => ({ data: url === '/admin/dest/status' ? destinationStatus({ nodes: [node] }) : values[url] }))
  const { copy } = await mount({}, 'zh-CN', vi.fn(), 'server')
  await screen.findByText(copy.hits_24h)
  expect(screen.getByText('71')).toBeTruthy()
  expect(screen.getByText(zh.access_control.coverage.collection_off)).toBeTruthy()
  expect(screen.getByTestId('coverage-losses-1').textContent).toContain('统计可能不完整')
  expect(screen.getByRole('link', { name: copy.view_records }).getAttribute('href')).toBe('/admin/access-control?tab=records&rec_panel=1')
  fireEvent.click(screen.getByRole('button', { name: copy.modify }))
  await screen.findByRole('dialog', { name: copy.title.replace('{{node}}', 'Tokyo') })
})

it('leaves unconfirmed node hit totals unknown in server details', async () => {
  const node = destinationNode({ supports: { policy: true, hits: true, usage: false }, collecting: false, hits_24h: null })
  api.get.mockImplementation(async (url: string) => ({ data: url === '/admin/dest/status' ? destinationStatus({ nodes: [node] }) : values[url] }))
  const { copy } = await mount({}, 'zh-CN', vi.fn(), 'server')
  await screen.findByText(copy.hits_24h)
  expect(screen.getByText('—')).toBeTruthy()
  expect(screen.getByText(zh.access_control.coverage.collection_unconfirmed)).toBeTruthy()
  expect(api.put).not.toHaveBeenCalled()
})

it('does not reopen a coverage collection draft for a different authenticated session', async () => {
  const { copy } = await mount({}, 'zh-CN', vi.fn(), 'coverage')
  fireEvent.click(screen.getByRole('button', { name: zh.access_control.coverage.more.replace('{{name}}', 'Tokyo') }))
  fireEvent.click(await screen.findByRole('menuitem', { name: copy.open }))
  await screen.findByRole('radio', { name: copy.hits })
  act(() => useAuthStore.setState({ userId: 77, authEpoch: 6 }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: copy.title.replace('{{node}}', 'Tokyo') })).toBeNull())
  expect(api.put).not.toHaveBeenCalled()
})
