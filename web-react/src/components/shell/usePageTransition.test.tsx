// @vitest-environment jsdom
import { cleanup, render } from '@testing-library/react'
import { useRef } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { PAGE_ENTER_MS, usePageTransition } from './usePageTransition'

function Probe({ k }: { k: string }) {
  const ref = useRef<HTMLElement>(null)
  usePageTransition(ref, k)
  return <main ref={ref}>x</main>
}

const animate = vi.fn()
beforeEach(() => {
  animate.mockReset()
  Element.prototype.animate = animate as never
  window.matchMedia = vi.fn().mockImplementation((q: string) => ({ matches: false, media: q })) as never
})
afterEach(cleanup)

describe('the page entrance', () => {
  it('does not play on first mount', () => {
    render(<Probe k="/admin/users" />)
    expect(animate).not.toHaveBeenCalled()
  })

  it('plays once per change of path, in 150ms', () => {
    const { rerender } = render(<Probe k="/admin/users" />)
    rerender(<Probe k="/admin/groups" />)
    expect(animate).toHaveBeenCalledTimes(1)
    expect(animate.mock.calls[0][1].duration).toBe(PAGE_ENTER_MS)
    rerender(<Probe k="/admin/groups" />)
    expect(animate).toHaveBeenCalledTimes(1)
  })

  it('is skipped under reduced motion', () => {
    window.matchMedia = vi.fn().mockImplementation((q: string) => ({ matches: q.includes('reduce'), media: q })) as never
    const { rerender } = render(<Probe k="/a" />)
    rerender(<Probe k="/b" />)
    expect(animate).not.toHaveBeenCalled()
  })
})
