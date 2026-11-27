// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { StatusBadge } from './StatusBadge'
import { StatusDot } from './StatusDot'

afterEach(cleanup)

describe('StatusBadge', () => {
  it('is a dot and plain words for a healthy or idle state', () => {
    for (const tone of ['success', 'neutral'] as const) {
      const { container } = render(<StatusBadge tone={tone} label="正常" />)
      expect(container.querySelector('[data-slot="status-badge"]')).toBeNull()
      expect(container.querySelector('[data-slot="status-dot"]')?.getAttribute('data-tone')).toBe(tone)
      expect(screen.getByText('正常')).toBeTruthy()
      cleanup()
    }
  })

  it('is a tinted badge with an icon for a warning, failure or notice', () => {
    for (const tone of ['warning', 'danger', 'info'] as const) {
      const { container } = render(<StatusBadge tone={tone} label="异常" testId="b" />)
      const badge = container.querySelector('[data-slot="status-badge"]') as HTMLElement
      expect(badge.getAttribute('data-tone')).toBe(tone)
      expect(badge.className).toContain('bg-')
      expect(badge.querySelector('svg')).toBeTruthy()
      // The words stay in the foreground colour; only the icon carries the hue.
      expect(badge.className).toContain('text-foreground')
      cleanup()
    }
  })

  it('lets the caller override the loudness either way', () => {
    const { container } = render(<><StatusBadge tone="danger" quiet label="a" /><StatusBadge tone="success" quiet={false} label="b" /></>)
    expect(container.querySelectorAll('[data-slot="status-badge"]')).toHaveLength(1)
    expect(container.querySelector('[data-slot="status-badge"]')?.textContent).toBe('b')
  })
})

describe('StatusDot', () => {
  it('never leaves the state to colour alone: the label is text', () => {
    render(<StatusDot tone="danger" label="离线" hideLabel />)
    expect(screen.getByText('离线').className).toContain('sr-only')
  })
})
