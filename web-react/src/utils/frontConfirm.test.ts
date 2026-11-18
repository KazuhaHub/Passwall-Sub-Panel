import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const confirmMock = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))
vi.mock('@/i18n', () => ({ default: { t: (key: string) => key } }))
vi.mock('@/api/client', () => ({ client: {} }))

import { withFrontConfirm } from './frontConfirm'

const refusal = (data: Record<string, unknown>, status = 409) => new AxiosError('x', String(status), undefined, undefined, {
  status, statusText: '', headers: {}, config: {} as never, data,
})

beforeEach(() => vi.clearAllMocks())

describe('withFrontConfirm', () => {
  it('passes a plain success through without asking', async () => {
    const run = vi.fn().mockResolvedValue('ok')
    expect(await withFrontConfirm(run)).toBe('ok')
    expect(run).toHaveBeenCalledTimes(1)
    expect(confirmMock).not.toHaveBeenCalled()
  })

  it('asks when a listener would join a shared port, then repeats the save with the offered loopback port', async () => {
    confirmMock.mockResolvedValue(true)
    const run = vi.fn()
      .mockRejectedValueOnce(refusal({ error: 'e', code: 'confirm_required', params: { action: 'join', loopback_port: 30044, port: 443 } }))
      .mockResolvedValueOnce('saved')
    expect(await withFrontConfirm(run)).toBe('saved')
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(run).toHaveBeenLastCalledWith({ loopback_port: 30044, confirm_front: true })
  })

  it('asks before a fronted listener is disabled and confirms with the flag', async () => {
    confirmMock.mockResolvedValue(true)
    const run = vi.fn()
      .mockRejectedValueOnce(refusal({ code: 'confirm_required', params: { action: 'disable', routes: ['a.example.com'] } }))
      .mockResolvedValueOnce('done')
    await withFrontConfirm(run)
    expect(run).toHaveBeenLastCalledWith({ loopback_port: 0, confirm_front: true })
  })

  it('asks before a fronted listener leaves its front, for a port change or a delete', async () => {
    confirmMock.mockResolvedValue(true)
    const run = vi.fn()
      .mockRejectedValueOnce(refusal({ code: 'confirm_required', params: { action: 'leave', routes: ['b.example.com'] } }))
      .mockResolvedValueOnce('done')
    await withFrontConfirm(run)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(run).toHaveBeenLastCalledWith({ loopback_port: 0, confirm_front: true })
  })

  it('asks once for a move between two shared ports, and before enabling next to a stopped front', async () => {
    confirmMock.mockResolvedValue(true)
    for (const action of ['move', 'enable']) {
      const run = vi.fn()
        .mockRejectedValueOnce(refusal({ code: 'confirm_required', params: { action, loopback_port: 30010 } }))
        .mockResolvedValueOnce('done')
      await withFrontConfirm(run)
      expect(run).toHaveBeenLastCalledWith({ loopback_port: 30010, confirm_front: true })
    }
    expect(confirmMock).toHaveBeenCalledTimes(2)
  })

  it('changes nothing when the admin declines', async () => {
    confirmMock.mockResolvedValue(false)
    const run = vi.fn().mockRejectedValue(refusal({ code: 'confirm_required', params: { action: 'join', loopback_port: 1 } }))
    await expect(withFrontConfirm(run)).rejects.toBeTruthy()
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('does not swallow any other failure', async () => {
    const run = vi.fn().mockRejectedValue(refusal({ code: 'port_conflict', params: {} }, 400))
    await expect(withFrontConfirm(run)).rejects.toBeTruthy()
    expect(confirmMock).not.toHaveBeenCalled()
    const other = vi.fn().mockRejectedValue(refusal({ code: 'confirm_required', params: { action: 'restricted' } }))
    await expect(withFrontConfirm(other)).rejects.toBeTruthy()
    expect(confirmMock).not.toHaveBeenCalled()
  })
})
