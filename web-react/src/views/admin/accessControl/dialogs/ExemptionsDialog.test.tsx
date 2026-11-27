// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { confirmMock, renderAccess, snack } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import ExemptionsDialog from './ExemptionsDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/components/UserAutocomplete', () => ({ default: () => <input aria-label="account" /> }))
vi.mock('@/query/users', () => ({ useUserDetail: () => ({ data: undefined }) }))

const H = 3_600_000
const ex = (over: Partial<api.DestExemption>): api.DestExemption => ({
  user_id: 1, upn: 'alice@corp', reason: 'debug', created_by: 9, created_by_upn: 'admin', created_at: Date.now() - H, expires_at: Date.now() + 23 * H, expired: false, ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestStatus).mockResolvedValue({ apply_eta_ms: 60_000, generation: 1, published_generation: 1, nodes: [], policy_notes: {}, totals: {} } as unknown as api.DestStatus)
})

describe('ExemptionsDialog', () => {
  it('lists rows, expired ones last, with expiring/permanent/expired words', async () => {
    vi.mocked(api.getDestExemptions).mockResolvedValue([
      ex({ user_id: 3, upn: 'old@corp', expires_at: Date.now() - H, expired: true }),
      ex({ user_id: 2, upn: 'ops-bot', reason: 'probe', expires_at: null }),
      ex({}),
    ])
    renderAccess(<ExemptionsDialog open onClose={() => {}} onOpenUser={() => {}} />)
    const rows = await screen.findAllByTestId('exemption-row')
    expect(rows.map(r => within(r).getByText(/@corp|ops-bot/).textContent)).toEqual(['ops-bot', 'alice@corp', 'old@corp'])
    expect(within(rows[0]).getByText('永久')).toBeTruthy()
    expect(within(rows[1]).getByText(/后到期/)).toBeTruthy()
    expect(within(rows[2]).getByText('已到期，等待清理（最多 1 小时）')).toBeTruthy()
  })

  it('empty state explains itself', async () => {
    vi.mocked(api.getDestExemptions).mockResolvedValue([])
    renderAccess(<ExemptionsDialog open onClose={() => {}} onOpenUser={() => {}} />)
    expect(await screen.findByText(/没有豁免的账号/)).toBeTruthy()
  })

  it('cancelling asks, then deletes', async () => {
    vi.mocked(api.getDestExemptions).mockResolvedValue([ex({})])
    vi.mocked(api.deleteDestExemption).mockResolvedValue()
    renderAccess(<ExemptionsDialog open onClose={() => {}} onOpenUser={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByText('取消豁免'))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '取消 alice@corp 的豁免？' })))
    await waitFor(() => expect(api.deleteDestExemption).toHaveBeenCalledWith(1))
    expect(snack).toHaveBeenCalled()
  })

  it('open account hands over to the drawer', async () => {
    vi.mocked(api.getDestExemptions).mockResolvedValue([ex({})])
    const onOpenUser = vi.fn()
    renderAccess(<ExemptionsDialog open onClose={() => {}} onOpenUser={onOpenUser} />)
    fireEvent.click(await screen.findByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByText('打开账号'))
    expect(onOpenUser).toHaveBeenCalledWith(1)
  })

  it('adds an exemption from the dialog it opens, over the sheet', async () => {
    vi.mocked(api.getDestExemptions).mockResolvedValue([])
    renderAccess(<ExemptionsDialog open onClose={() => {}} onOpenUser={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '添加豁免' }))
    expect(await screen.findByRole('dialog', { name: '添加豁免' })).toBeTruthy()
  })

  it('the confirmation to cancel an exemption starts on Cancel', async () => {
    vi.mocked(api.getDestExemptions).mockResolvedValue([ex({})])
    vi.mocked(api.deleteDestExemption).mockResolvedValue()
    renderAccess(<ExemptionsDialog open onClose={() => {}} onOpenUser={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByText('取消豁免'))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ destructive: true, initialFocus: 'cancel' })))
  })
})
