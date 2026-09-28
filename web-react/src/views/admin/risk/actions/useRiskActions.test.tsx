/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import type { QueryClient } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { AxiosError, type AxiosResponse } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import { alertKeys, riskCenterKeys, userKeys } from '@/query/keys'
import { sessionScope } from '@/query/session'
import type { AttentionEntry, ReviewBadge } from '@/api/riskCenter'
import type { RiskActionKind, RiskSubject } from './riskSubject'
import { useRiskActions } from './useRiskActions'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const snack = vi.hoisted(() => vi.fn())
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack, default: () => null }))
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
import zhCommon from '@/locales/zh-CN/common.json'
import { flatten, type Nested } from '@/i18n/options'
dict.current = {
  ...flatten(zh as Nested),
  ...Object.fromEntries(Object.entries(flatten(zhCommon as Nested)).map(([k, v]) => [`common:${k}`, v])),
}

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
const SKIP = { _skipErrorToast: true }
const NO_REVIEW: ReviewBadge = { dismissed: false, reopened: false, lapsed: false, trusted: false, escalated: [] }
const GEO: AttentionEntry[] = [{ source: 'geo', level: 'flagged' }]
const REVIEW_DTO = { dismissed: false, reopened: false, lapsed: false, escalated: [], trusted: true }

function subject(over: Partial<RiskSubject> = {}): RiskSubject {
  return { id: 7, upn: 'alice', service_state: 'active', service_disabled_reason: '', attention: GEO,
    review: NO_REVIEW, ...over }
}

const geoAutoHeld = (over: Partial<RiskSubject> = {}) => subject({
  service_state: 'manual_suspended', service_disabled_reason: 'geo_auto',
  attention: [...GEO, { source: 'geo_auto', level: 'suspended' }], ...over,
})

const KINDS: RiskActionKind[] = ['pause', 'convert_manual', 'resume', 'dismiss', 'redismiss', 'undismiss', 'trust', 'untrust']

function Harness({ subject: s }: { subject: RiskSubject }) {
  const a = useRiskActions()
  return (
    <>
      {KINDS.map(k => <button key={k} onClick={() => a.start(k, s)}>{`start-${k}`}</button>)}
      {a.dialogs}
    </>
  )
}

function mount(s: RiskSubject, client: QueryClient = makeTestQueryClient()) {
  render(<ThemeProvider theme={theme}><Harness subject={s} /></ThemeProvider>, { wrapper: queryWrapper(client) })
  return client
}

function httpError(status: number, data: unknown) {
  return new AxiosError('failed', 'ERR_BAD_REQUEST', undefined, undefined,
    { status, data, statusText: '', headers: {}, config: {} } as AxiosResponse)
}

/** Starts an action and returns its dialog. */
async function start(kind: RiskActionKind) {
  fireEvent.click(screen.getByText(`start-${kind}`))
  return screen.findByRole('dialog')
}

function confirmIn(dialog: HTMLElement, name: string) {
  fireEvent.click(within(dialog).getByRole('button', { name }))
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  useAuthStore.setState({ role: '' })
})

describe('useRiskActions: service actions', () => {
  it('pauses a geo account as an admin geo hold, the note as the detail', async () => {
    api.post.mockResolvedValue({ data: undefined })
    mount(subject())
    const dlg = await start('pause')
    expect(within(dlg).getByText(/状态记为「异地人工暂停」/)).toBeTruthy()
    const note = within(dlg).getByRole('textbox', { name: '告知用户的说明（可选）' }) as HTMLInputElement
    // Never prefilled: an empty detail keeps the mail's own wording.
    expect(note.value).toBe('')
    fireEvent.change(note, { target: { value: 'please get in touch' } })
    confirmIn(dlg, '暂停代理服务')

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/users/7/set-service-status',
      { enabled: false, reason: 'geo_anomaly', detail: 'please get in touch' }, SKIP))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已暂停代理服务', 'success'))
  })

  it('pauses a devices-only account with the Users page reason', async () => {
    api.post.mockResolvedValue({ data: undefined })
    mount(subject({ attention: [{ source: 'devices', level: 'flagged' }] }))
    const dlg = await start('pause')
    expect(within(dlg).getByText(/状态记为「代理服务暂停」/)).toBeTruthy()
    confirmIn(dlg, '暂停代理服务')
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/users/7/set-service-status',
      { enabled: false, reason: 'service_manual' }, SKIP))
  })

  it('says the pause note reaches the user', async () => {
    mount(subject())
    const dlg = await start('pause')
    expect(within(dlg).getByText('会显示在用户门户和邮件中；不要写地点、IP 或其他用户。')).toBeTruthy()
  })

  it('turns the detector hold into an admin one', async () => {
    api.post.mockResolvedValue({ data: undefined })
    mount(geoAutoHeld())
    const dlg = await start('convert_manual')
    confirmIn(dlg, '改为人工暂停')
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/users/7/set-service-status',
      { enabled: false, reason: 'geo_anomaly' }, SKIP))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已改为人工暂停', 'success'))
  })

  it('resumes only the hold it showed', async () => {
    api.post.mockResolvedValue({ data: undefined })
    mount(subject({ service_state: 'manual_suspended', service_disabled_reason: 'geo_anomaly' }))
    const dlg = await start('resume')
    expect(within(dlg).getByText(/清除「异地人工暂停」暂停/)).toBeTruthy()
    // Only the detector's own hold warns and offers trust.
    expect(within(dlg).queryByRole('checkbox')).toBeNull()
    confirmIn(dlg, '恢复代理服务')
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/users/7/set-service-status',
      { enabled: true, expect_reason: 'geo_anomaly' }, SKIP))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已恢复代理服务', 'success'))
  })

  it('a hold changed meanwhile is left alone and said so, and the views refresh', async () => {
    api.post.mockRejectedValue(httpError(409, { error: 'Service state changed', code: 'reason_changed' }))
    const client = makeTestQueryClient()
    const spy = vi.spyOn(client, 'invalidateQueries')
    mount(subject({ service_state: 'manual_suspended', service_disabled_reason: 'service_manual' }), client)
    const dlg = await start('resume')
    confirmIn(dlg, '恢复代理服务')
    await waitFor(() => expect(snack).toHaveBeenCalledWith('暂停原因已被更改，已刷新', 'info'))
    const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: useAuthStore.getState().authEpoch })
    expect(spy).toHaveBeenCalledWith({ queryKey: riskCenterKeys.all(scope) })
  })

  it('resuming the detector hold warns and can trust at once', async () => {
    api.post.mockResolvedValue({ data: { review: REVIEW_DTO, resumed: true } })
    mount(geoAutoHeld())
    const dlg = await start('resume')
    expect(within(dlg).getByText(/满足暂停条件时会再次自动暂停/)).toBeTruthy()
    const also = within(dlg).getByRole('checkbox', { name: '同时信任此账号' }) as HTMLInputElement
    expect(also.checked).toBe(false)
    fireEvent.click(also)
    confirmIn(dlg, '恢复代理服务')

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/risk-center/users/7/trust',
      { resume_service: true }, SKIP))
    expect(api.post.mock.calls.some(([u]) => String(u).endsWith('/set-service-status'))).toBe(false)
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已信任此账号并恢复代理服务', 'success'))
  })
})

describe('useRiskActions: review actions', () => {
  it('dismiss sends the levels the admin saw', async () => {
    api.post.mockResolvedValue({ data: { review: REVIEW_DTO } })
    mount(subject({ attention: [...GEO, { source: 'devices', level: 'suspect' }] }))
    const dlg = await start('dismiss')
    expect(within(dlg).getByText('长期、确认无害的多地使用，请改用「信任此账号」。')).toBeTruthy()
    fireEvent.change(within(dlg).getByRole('textbox', { name: '备注（可选，仅管理员可见）' }),
      { target: { value: 'known traveller' } })
    confirmIn(dlg, '忽略')
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/risk-center/users/7/dismiss',
      { note: 'known traveller', expected: { geo: 'flagged', devices: 'suspect' } }, SKIP))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已忽略', 'success'))
  })

  it('dismissing again says so on its button', async () => {
    api.post.mockResolvedValue({ data: { review: REVIEW_DTO } })
    mount(subject({ review: { ...NO_REVIEW, dismissed: true, reopened: true, escalated: ['geo'] } }))
    const dlg = await start('redismiss')
    confirmIn(dlg, '再次忽略')
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/risk-center/users/7/dismiss',
      { expected: { geo: 'flagged' } }, SKIP))
  })

  it.each([
    ['nothing_to_dismiss', '当前没有需要忽略的信号，已刷新'],
    ['changed', '信号刚刚有了变化，已刷新，请确认后再操作'],
    ['already_dismissed', '状态已被其他人更改，已刷新'],
    ['not_dismissed', '状态已被其他人更改，已刷新'],
    ['already_trusted', '状态已被其他人更改，已刷新'],
    ['not_trusted', '状态已被其他人更改，已刷新'],
  ])('409 %s is said in its own words', async (code, text) => {
    api.post.mockRejectedValue(httpError(409, { error: 'conflict', code }))
    mount(subject())
    const dlg = await start('dismiss')
    confirmIn(dlg, '忽略')
    await waitFor(() => expect(snack).toHaveBeenCalledWith(text, 'info'))
  })

  it('a note over the limit is refused in words', async () => {
    api.post.mockRejectedValue(httpError(400, { error: 'Note too long', code: 'note_too_long' }))
    mount(subject())
    const dlg = await start('dismiss')
    confirmIn(dlg, '忽略')
    await waitFor(() => expect(snack).toHaveBeenCalledWith('最多 200 字', 'error'))
  })

  it('any other failure is reported once, by the action', async () => {
    api.delete.mockRejectedValue(httpError(500, { error: 'db down' }))
    mount(subject({ review: { ...NO_REVIEW, dismissed: true } }))
    const dlg = await start('undismiss')
    confirmIn(dlg, '取消忽略')
    await waitFor(() => expect(snack).toHaveBeenCalledWith('db down', 'error'))
  })

  it('trust on a held account resumes by default', async () => {
    api.post.mockResolvedValue({ data: { review: REVIEW_DTO, resumed: true } })
    mount(geoAutoHeld())
    const dlg = await start('trust')
    const resume = within(dlg).getByRole('checkbox', { name: '同时恢复代理服务（当前为异地自动暂停）' }) as HTMLInputElement
    expect(resume.checked).toBe(true)
    confirmIn(dlg, '信任此账号')
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/risk-center/users/7/trust',
      { resume_service: true }, SKIP))
  })

  it('trust on an unheld account does not offer a resume', async () => {
    api.post.mockResolvedValue({ data: { review: REVIEW_DTO, resumed: false } })
    mount(subject())
    const dlg = await start('trust')
    expect(within(dlg).queryByRole('checkbox')).toBeNull()
    confirmIn(dlg, '信任此账号')
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/risk-center/users/7/trust',
      { resume_service: false }, SKIP))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已信任此账号', 'success'))
  })

  it.each([
    [{ resumed: true, resume_warning: 'queue down' }, '已信任并恢复代理服务，但推送到面板失败且未能排队重试：queue down'],
    [{ resumed: false, resume_error: 'panel down' }, '已信任，但恢复代理服务失败：panel down'],
  ])('a trust whose resume went wrong warns (%j)', async (res, text) => {
    api.post.mockResolvedValue({ data: { review: REVIEW_DTO, ...res } })
    mount(geoAutoHeld())
    const dlg = await start('trust')
    confirmIn(dlg, '信任此账号')
    await waitFor(() => expect(snack).toHaveBeenCalledWith(text, 'warning'))
  })

  it('untrust and undismiss ask first, then call their routes', async () => {
    api.delete.mockResolvedValue({ data: { review: REVIEW_DTO } })
    mount(subject({ review: { ...NO_REVIEW, dismissed: true, trusted: true } }))
    let dlg = await start('untrust')
    expect(within(dlg).getByText('下一轮检测起按分组策略重新判定。')).toBeTruthy()
    confirmIn(dlg, '取消信任')
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/risk-center/users/7/trust', SKIP))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已取消信任', 'success'))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    dlg = await start('undismiss')
    confirmIn(dlg, '取消忽略')
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/risk-center/users/7/dismiss', SKIP))
  })

  it('a success refreshes the risk center, the users and the bell', async () => {
    api.delete.mockResolvedValue({ data: { review: REVIEW_DTO } })
    const client = makeTestQueryClient()
    const spy = vi.spyOn(client, 'invalidateQueries')
    mount(subject({ review: { ...NO_REVIEW, dismissed: true } }), client)
    const dlg = await start('undismiss')
    confirmIn(dlg, '取消忽略')
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已取消忽略', 'success'))
    const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: useAuthStore.getState().authEpoch })
    expect(spy).toHaveBeenCalledWith({ queryKey: riskCenterKeys.all(scope) })
    expect(spy).toHaveBeenCalledWith({ queryKey: userKeys.all(scope) })
    expect(spy).toHaveBeenCalledWith({ queryKey: alertKeys.all(scope) })
  })

  it('cancel closes the dialog and calls nothing', async () => {
    mount(subject())
    const dlg = await start('dismiss')
    fireEvent.click(within(dlg).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(api.post).not.toHaveBeenCalled()
  })
})
