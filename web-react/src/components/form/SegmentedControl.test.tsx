// @vitest-environment jsdom
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SegmentedControl } from './SegmentedControl'

afterEach(cleanup)

const options = [
  { value: 'a', label: 'Alpha' },
  { value: 'b', label: 'Beta', disabled: true },
  { value: 'c', label: 'Gamma' },
]

describe('SegmentedControl', () => {
  it('is a radio group with the current value checked and one tab stop', () => {
    render(<SegmentedControl aria-label="Mode" value="a" onChange={() => {}} options={options} />)
    expect(screen.getByRole('radiogroup', { name: 'Mode' })).toBeTruthy()
    expect(screen.getByRole('radio', { name: 'Alpha' }).getAttribute('aria-checked')).toBe('true')
    const tabbable = screen.getAllByRole('radio').filter(r => r.getAttribute('tabindex') === '0')
    expect(tabbable).toHaveLength(1)
  })

  it('selects on click', () => {
    const onChange = vi.fn()
    render(<SegmentedControl aria-label="Mode" value="a" onChange={onChange} options={options} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Gamma' }))
    expect(onChange).toHaveBeenCalledWith('c')
  })

  it('moves with the arrow keys and skips a disabled option', () => {
    const onChange = vi.fn()
    render(<SegmentedControl aria-label="Mode" value="a" onChange={onChange} options={options} />)
    fireEvent.keyDown(screen.getByRole('radio', { name: 'Alpha' }), { key: 'ArrowRight' })
    expect(onChange).toHaveBeenCalledWith('c')
    fireEvent.keyDown(screen.getByRole('radio', { name: 'Alpha' }), { key: 'ArrowLeft' })
    expect(onChange).toHaveBeenLastCalledWith('c')
  })

  it('does not select a disabled option', () => {
    const onChange = vi.fn()
    render(<SegmentedControl aria-label="Mode" value="a" onChange={onChange} options={options} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Beta' }))
    expect(onChange).not.toHaveBeenCalled()
  })
})

describe('SegmentedControl contrast', () => {
  it('draws unselected options in the muted token and the selected one raised on its own surface', () => {
    render(<SegmentedControl aria-label="Mode" value="a" onChange={() => {}} options={options} />)
    expect(screen.getByRole('radio', { name: 'Gamma' }).className).toContain('text-muted-foreground')
    const selected = screen.getByRole('radio', { checked: true })
    expect(selected.className).toMatch(/bg-segment-active/)
    expect(selected.className).toMatch(/font-semibold/)
    expect(selected.className).toMatch(/ring-segment-border/)
  })

  // The muted grey is what an unselected option is drawn in, on the track: it must stay readable (4.5:1) in both themes.
  it.each([['light', /(?:^|\n):root\s*\{([^}]*--background[^}]*)\}/], ['dark', /(?:^|\n)\.dark\s*\{([^}]*)\}/]] as const)('muted text on the %s track is at least 4.5:1', (_, re) => {
    const css = readFileSync(resolve(process.cwd(), 'src/styles/ui.css'), 'utf8')
    const decl = (name: string) => new RegExp(`${name}:\\s*oklch\\(([\\d.]+)`).exec(re.exec(css)![1])![1]
    const y = (L: number) => { const v = L ** 3; return v } // a grey: linear luminance is L cubed
    const [a, b] = [y(Number(decl('--muted-foreground'))), y(Number(decl('--segment-track')))]
    expect((Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)).toBeGreaterThanOrEqual(4.5)
  })
})
