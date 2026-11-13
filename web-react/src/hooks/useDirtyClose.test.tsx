// @vitest-environment jsdom
import { renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const confirmMock = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))

import { useDirtyClose } from './useDirtyClose'

const copy = { title: 'Discard?', message: 'Nothing is saved.' }
beforeEach(() => confirmMock.mockReset())

describe('useDirtyClose', () => {
  it('closes at once, without a prompt, when nothing is unsaved', () => {
    const close = vi.fn()
    const { result } = renderHook(() => useDirtyClose(false, copy))
    result.current(close)
    expect(close).toHaveBeenCalledTimes(1)
    expect(confirmMock).not.toHaveBeenCalled()
  })

  it('asks first when dirty and closes only on a yes', async () => {
    const close = vi.fn()
    confirmMock.mockResolvedValueOnce(false).mockResolvedValueOnce(true)
    const { result } = renderHook(() => useDirtyClose(true, copy))
    result.current(close)
    await Promise.resolve(); await Promise.resolve()
    expect(confirmMock).toHaveBeenCalledWith(copy)
    expect(close).not.toHaveBeenCalled()
    result.current(close)
    await Promise.resolve(); await Promise.resolve()
    expect(close).toHaveBeenCalledTimes(1)
  })
})
