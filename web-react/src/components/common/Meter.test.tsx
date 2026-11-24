// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { Meter } from './Meter'

afterEach(cleanup)

it('is a progressbar with its value and range when it has a label', () => {
  render(<Meter value={30} max={120} label="Traffic" />)
  const bar = screen.getByRole('progressbar', { name: 'Traffic' })
  expect(bar.getAttribute('aria-valuemax')).toBe('120')
  expect(bar.getAttribute('aria-valuenow')).toBe('30')
  expect((bar.firstChild as HTMLElement).style.width).toBe('25%')
})

it('is hidden from assistive tech without a label, and never draws past either end', () => {
  const { container } = render(<><Meter value={500} max={100} /><Meter value={-4} max={100} /><Meter value={5} max={0} /></>)
  expect(screen.queryByRole('progressbar')).toBeNull()
  const widths = [...container.querySelectorAll('[aria-hidden="true"] > div')].map(d => (d as HTMLElement).style.width)
  expect(widths).toEqual(['100%', '0%', '0%'])
})
