// @vitest-environment jsdom
import { renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { LOG_IN_BAR_MIN_WIDTH, useLogInBar } from './useLogInBar'

afterEach(() => { vi.unstubAllGlobals() })

// The bar-or-menu decision lives in JavaScript only, so there is no stylesheet whose breakpoint
// could silently disagree with it. These pin that: the number, the query it becomes, and that no
// other file declares a second 480px breakpoint that would put a button and a menu item (or
// neither) on screen in the same band.
describe('the width at which the log button moves into the account menu', () => {
  it('is 480px', () => {
    expect(LOG_IN_BAR_MIN_WIDTH).toBe(480)
  })

  it('asks for exactly (min-width: 480px)', () => {
    const queries: string[] = []
    vi.stubGlobal('matchMedia', (q: string) => {
      queries.push(q)
      return { matches: true, addEventListener() {}, removeEventListener() {} }
    })
    const { result } = renderHook(() => useLogInBar())
    expect(result.current).toBe(true)
    expect(new Set(queries)).toEqual(new Set(['(min-width: 480px)']))
  })

  it('has no second 480px breakpoint anywhere else in the source (styles or scripts)', () => {
    const files = import.meta.glob<string>(['../**/*.{ts,tsx,css}', '!../**/*.test.*', '!../locales/**'], { query: '?raw', import: 'default', eager: true })
    const offenders = Object.entries(files)
      .filter(([path, text]) => !path.endsWith('/useLogInBar.ts') && /(?:min|max)-width:\s*(?:4[78]\d|480)px|\[(?:min|max)-?\[?480px|(?:min|max)-\[48\dpx\]|min-\[480px\]|max-\[480px\]/.test(text))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })
})
