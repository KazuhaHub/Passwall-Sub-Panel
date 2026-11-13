// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { renderAccess, snack } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import AddExemptionDialog from './AddExemptionDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/components/UserAutocomplete', () => ({ default: () => <input aria-label="account" /> }))
vi.mock('@/query/users', () => ({ useUserDetail: () => ({ data: { id: 7, upn: 'alice@corp' } }) }))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestStatus).mockResolvedValue({ apply_eta_ms: 60_000, generation: 1, published_generation: 1, nodes: [], policy_notes: {}, totals: {} } as unknown as api.DestStatus)
})

describe('AddExemptionDialog', () => {
  it('requires a reason, then creates a permanent exemption for the locked account', async () => {
    vi.mocked(api.createDestExemption).mockResolvedValue({} as api.DestExemption)
    const onClose = vi.fn()
    renderAccess(<AddExemptionDialog open onClose={onClose} lockedUserId={7} />)
    fireEvent.click(screen.getByRole('button', { name: '添加' }))
    expect(await screen.findByText('请填写原因')).toBeTruthy()
    expect(api.createDestExemption).not.toHaveBeenCalled()
    fireEvent.change(screen.getByRole('textbox', { name: /原因/ }), { target: { value: 'debug' } })
    fireEvent.click(screen.getByRole('button', { name: '添加' }))
    await waitFor(() => expect(api.createDestExemption).toHaveBeenCalledWith({ user_id: 7, reason: 'debug', expires_at: null }))
    expect(snack).toHaveBeenCalledWith('已豁免 alice@corp，将随下一次下发生效', 'success')
    expect(onClose).toHaveBeenCalled()
  })

  it('24 hours sends an expiry about a day out and explains the lag', async () => {
    vi.mocked(api.createDestExemption).mockResolvedValue({} as api.DestExemption)
    renderAccess(<AddExemptionDialog open onClose={() => {}} lockedUserId={7} />)
    fireEvent.change(screen.getByRole('textbox', { name: /原因/ }), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: '24 小时' }))
    expect(screen.getByText('到期后最迟约 1 小时加一个下发窗口才真正失效')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '添加' }))
    await waitFor(() => expect(api.createDestExemption).toHaveBeenCalled())
    const sent = vi.mocked(api.createDestExemption).mock.calls[0][0].expires_at as number
    expect(Math.abs(sent - (Date.now() + 86_400_000))).toBeLessThan(5000)
  })

  it('a duplicate shows on the account field', async () => {
    const err = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_exemption_exists' } } })
    vi.mocked(api.createDestExemption).mockRejectedValue(err)
    renderAccess(<AddExemptionDialog open onClose={() => {}} lockedUserId={7} />)
    fireEvent.change(screen.getByRole('textbox', { name: /原因/ }), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: '添加' }))
    expect(await screen.findByText('这个账号已被豁免')).toBeTruthy()
  })

  it('edit updates the existing exemption', async () => {
    vi.mocked(api.updateDestExemption).mockResolvedValue({} as api.DestExemption)
    const existing = { user_id: 7, upn: 'alice@corp', reason: 'old', created_by: 1, created_by_upn: 'admin', created_at: 1, expires_at: null, expired: false }
    renderAccess(<AddExemptionDialog open onClose={() => {}} existing={existing} />)
    fireEvent.change(screen.getByRole('textbox', { name: /原因/ }), { target: { value: 'new' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.updateDestExemption).toHaveBeenCalledWith(7, { reason: 'new', expires_at: null }))
  })
})
