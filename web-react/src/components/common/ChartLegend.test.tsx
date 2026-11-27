// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { ChartLegend } from './ChartLegend'

afterEach(cleanup)

it('is one toggle button per series, pressed unless hidden, with the pattern of its line', () => {
  const onToggle = vi.fn()
  render(<ChartLegend hidden={new Set(['Down'])} onToggle={onToggle}
    items={[{ name: 'Up', color: 'red', dash: 'solid' }, { name: 'Down', color: 'blue', dash: 'dashed' }]} />)
  expect(screen.getByRole('button', { name: 'Up' }).getAttribute('aria-pressed')).toBe('true')
  const down = screen.getByRole('button', { name: 'Down' })
  expect(down.getAttribute('aria-pressed')).toBe('false')
  expect(down.querySelector('[data-slot="chart-swatch"]')!.getAttribute('data-dash')).toBe('dashed')
  fireEvent.click(down)
  expect(onToggle).toHaveBeenCalledWith('Down')
})
