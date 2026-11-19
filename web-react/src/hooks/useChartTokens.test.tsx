// @vitest-environment jsdom
import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { chartBaseOption, readChartTokens, useChartTokens } from './useChartTokens'

const root = document.documentElement

beforeEach(() => {
  // jsdom has no canvas: say so quietly instead of logging "not implemented".
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)
  root.removeAttribute('style')
  root.className = ''
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('readChartTokens', () => {
  it('reads colours from the CSS variables on <html>', () => {
    root.style.setProperty('--brand', '#112233')
    root.style.setProperty('--muted-foreground', '#445566')
    root.style.setProperty('--series-2', '#abcdef')
    const t = readChartTokens(root)
    expect(t.brand).toBe('#112233')
    expect(t.mutedText).toBe('#445566')
    expect(t.series[1]).toBe('#abcdef')
    expect(t.series).toHaveLength(8)
  })

  it('never returns an empty colour, so a missing variable cannot blank a chart', () => {
    const t = readChartTokens(root)
    for (const c of [t.text, t.mutedText, t.grid, t.tooltipBg, t.brand, ...t.series]) expect(c).toMatch(/\S/)
  })
})

describe('useChartTokens', () => {
  it('updates when the dark class flips', async () => {
    root.style.setProperty('--foreground', '#000000')
    const { result } = renderHook(() => useChartTokens())
    expect(result.current.text).toBe('#000000')
    await act(async () => {
      root.style.setProperty('--foreground', '#ffffff')
      root.classList.add('dark')
      await Promise.resolve()
    })
    expect(result.current.text).toBe('#ffffff')
  })

  it('updates when the brand colour changes (preset switch or a custom colour)', async () => {
    root.style.setProperty('--brand', '#ff0000')
    const { result } = renderHook(() => useChartTokens())
    expect(result.current.brand).toBe('#ff0000')
    await act(async () => {
      root.dataset.brand = 'teal'
      root.style.setProperty('--brand', '#00aa00')
      await Promise.resolve()
    })
    expect(result.current.brand).toBe('#00aa00')
  })

  it('keeps the same object while nothing changed', () => {
    const { result, rerender } = renderHook(() => useChartTokens())
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })
})

describe('chartBaseOption', () => {
  it('turns tokens into the axis, tooltip and text options every chart repeats', () => {
    root.style.setProperty('--popover', '#101010')
    const o = chartBaseOption(readChartTokens(root))
    expect(o.tooltip.backgroundColor).toBe('#101010')
    expect(o.color).toHaveLength(8)
    expect(o.backgroundColor).toBe('transparent')
  })
})

describe('useChartTokens repaint control', () => {
  it('keeps the same object when the attribute that changed did not change any colour', async () => {
    root.style.setProperty('--brand', '#112233')
    const { result } = renderHook(() => useChartTokens())
    const first = result.current
    await act(async () => {
      root.dataset.density = 'comfortable'
      root.classList.add('unrelated')
      await Promise.resolve()
    })
    expect(result.current).toBe(first)
  })

  it('hands out a new object when a colour changed', async () => {
    root.style.setProperty('--brand', '#112233')
    const { result } = renderHook(() => useChartTokens())
    const first = result.current
    await act(async () => { root.style.setProperty('--brand', '#445566'); await Promise.resolve() })
    expect(result.current).not.toBe(first)
  })
})
