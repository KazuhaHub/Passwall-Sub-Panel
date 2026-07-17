import { describe, expect, it } from 'vitest'

import { allSettledLimited } from './promises'

describe('allSettledLimited', () => {
  it('caps concurrency and preserves input order', async () => {
    let active = 0
    let peak = 0
    const results = await allSettledLimited([30, 5, 20, 1], async (delay, index) => {
      active++
      peak = Math.max(peak, active)
      await new Promise(resolve => setTimeout(resolve, delay))
      active--
      return index
    }, 2)

    expect(peak).toBe(2)
    expect(results).toEqual([
      { status: 'fulfilled', value: 0 },
      { status: 'fulfilled', value: 1 },
      { status: 'fulfilled', value: 2 },
      { status: 'fulfilled', value: 3 },
    ])
  })

  it('settles rejections and continues processing', async () => {
    const failure = new Error('failed')
    const results = await allSettledLimited([1, 2, 3], value => {
      if (value === 2) throw failure
      return value * 2
    }, 1)

    expect(results[0]).toEqual({ status: 'fulfilled', value: 2 })
    expect(results[1]).toEqual({ status: 'rejected', reason: failure })
    expect(results[2]).toEqual({ status: 'fulfilled', value: 6 })
  })

  it('handles an empty input and rejects invalid limits', async () => {
    await expect(allSettledLimited([], () => 1)).resolves.toEqual([])
    await expect(allSettledLimited([1], () => 1, 0)).rejects.toThrow(RangeError)
    await expect(allSettledLimited([1], () => 1, Number.POSITIVE_INFINITY)).rejects.toThrow(RangeError)
  })
})
