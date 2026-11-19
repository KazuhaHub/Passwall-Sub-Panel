// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FacetFilter } from './FacetFilter'

afterEach(cleanup)

const options = [
  { value: 'up', label: 'Online', count: 12 },
  { value: 'down', label: 'Offline', count: 3 },
  { value: 'pending', label: 'Pending', count: 0 },
]

describe('FacetFilter', () => {
  it('shows only its name while nothing is chosen', () => {
    render(<FacetFilter label="Status" options={options} selected={[]} onChange={() => {}} />)
    expect(screen.getByRole('button', { name: /^Status$/ })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /Clear/ })).toBeNull()
  })

  it('opens a checklist with a count beside every option', () => {
    render(<FacetFilter label="Status" options={options} selected={[]} onChange={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /^Status$/ }))
    expect(screen.getByRole('checkbox', { name: /Online/ }).textContent).toBeDefined()
    expect(screen.getByText('12')).toBeTruthy()
    expect(screen.getByText('3')).toBeTruthy()
  })

  it('toggles a value on and off', () => {
    const onChange = vi.fn()
    const { rerender } = render(<FacetFilter label="Status" options={options} selected={[]} onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: /^Status$/ }))
    fireEvent.click(screen.getByRole('checkbox', { name: /Online/ }))
    expect(onChange).toHaveBeenLastCalledWith(['up'])
    rerender(<FacetFilter label="Status" options={options} selected={['up']} onChange={onChange} />)
    fireEvent.click(screen.getByRole('checkbox', { name: /Online/ }))
    expect(onChange).toHaveBeenLastCalledWith([])
  })

  it('summarises the selection on the button: the one chosen, or the first and how many more', () => {
    const { rerender } = render(<FacetFilter label="Status" options={options} selected={['up']} onChange={() => {}} />)
    expect(screen.getByRole('button', { name: /^Status/ }).textContent).toContain('Online')
    rerender(<FacetFilter label="Status" options={options} selected={['up', 'down']} onChange={() => {}} />)
    expect(screen.getByRole('button', { name: /^Status/ }).textContent).toContain('Online and 1 more')
  })

  it('clears with its own button, without opening the list', () => {
    const onChange = vi.fn()
    render(<FacetFilter label="Status" options={options} selected={['up', 'down']} onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: 'Clear Status' }))
    expect(onChange).toHaveBeenCalledWith([])
    expect(screen.queryByRole('checkbox')).toBeNull()
  })

  it('searches a long list of options', () => {
    const many = Array.from({ length: 12 }, (_, i) => ({ value: `v${i}`, label: i === 9 ? 'Tokyo edge' : `Region ${i}`, count: i }))
    render(<FacetFilter label="Region" options={many} selected={[]} onChange={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /^Region$/ }))
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search options' }), { target: { value: 'tokyo' } })
    expect(screen.getAllByRole('checkbox')).toHaveLength(1)
  })

  it('says how many in all in Chinese: three chosen reads "等 3 项", not "等 2 项"', async () => {
    const i18n = (await import('@/test/initI18n')).default
    await i18n.changeLanguage('zh-CN')
    try {
      const many = [...options, { value: 'x', label: 'Extra', count: 1 }]
      render(<FacetFilter label="状态" options={many} selected={['up', 'down', 'pending']} onChange={() => {}} />)
      expect(screen.getByRole('button', { name: /^状态/ }).textContent).toContain('Online 等 3 项')
    } finally {
      await i18n.changeLanguage('en-US')
    }
  })
})
