// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NumberField } from './NumberField'

afterEach(cleanup)

function Harness(props: { initial: number | null; onValue?: (v: number | null) => void } & Partial<React.ComponentProps<typeof NumberField>>) {
  const { initial, onValue, ...rest } = props
  const [v, setV] = useState<number | null>(initial)
  return <NumberField aria-label="Limit" value={v} onChange={n => { setV(n); onValue?.(n) }} {...rest} />
}

describe('NumberField', () => {
  it('shows the unit as a suffix and reports numbers', () => {
    const onValue = vi.fn()
    render(<Harness initial={5} unit="GB" onValue={onValue} />)
    expect(screen.getByText('GB')).toBeTruthy()
    fireEvent.change(screen.getByLabelText('Limit'), { target: { value: '12' } })
    expect(onValue).toHaveBeenLastCalledWith(12)
  })

  it('reports null for an empty box, never 0', () => {
    const onValue = vi.fn()
    render(<Harness initial={5} onValue={onValue} />)
    fireEvent.change(screen.getByLabelText('Limit'), { target: { value: '' } })
    expect(onValue).toHaveBeenLastCalledWith(null)
  })

  it('keeps typing that is not a number out of the box', () => {
    render(<Harness initial={5} integer />)
    const input = screen.getByLabelText('Limit') as HTMLInputElement
    fireEvent.change(input, { target: { value: '5x' } })
    expect(input.value).toBe('5')
    fireEvent.change(input, { target: { value: '5.5' } })
    expect(input.value).toBe('5')
  })

  it('accepts a decimal when integers are not required', () => {
    const onValue = vi.fn()
    render(<Harness initial={null} onValue={onValue} />)
    fireEvent.change(screen.getByLabelText('Limit'), { target: { value: '1.5' } })
    expect(onValue).toHaveBeenLastCalledWith(1.5)
  })

  describe('with an empty-means-follow affordance', () => {
    it('makes the empty state visible and different from zero', () => {
      const { rerender } = render(<NumberField aria-label="Limit" value={null} onChange={() => {}} allowEmpty emptyLabel="Follows the group" />)
      expect(screen.getByText('Follows the group')).toBeTruthy()
      expect((screen.getByLabelText('Limit') as HTMLInputElement).value).toBe('')
      rerender(<NumberField aria-label="Limit" value={0} onChange={() => {}} allowEmpty emptyLabel="Follows the group" zeroLabel="Unlimited" />)
      expect(screen.queryByText('Follows the group')).toBeNull()
      expect((screen.getByLabelText('Limit') as HTMLInputElement).value).toBe('0')
      expect(screen.getByText('Unlimited')).toBeTruthy()
    })

    it('goes back to following with one click, which reports null and not 0', () => {
      const onValue = vi.fn()
      render(<Harness initial={0} allowEmpty emptyLabel="Follows the group" onValue={onValue} />)
      fireEvent.click(screen.getByRole('button', { name: 'Follow again' }))
      expect(onValue).toHaveBeenLastCalledWith(null)
      expect(screen.getByText('Follows the group')).toBeTruthy()
    })

    it('offers no follow button without allowEmpty', () => {
      render(<Harness initial={3} />)
      expect(screen.queryByRole('button', { name: 'Follow again' })).toBeNull()
    })
  })

  it('flags a value outside min/max for the page to explain', () => {
    render(<Harness initial={500} min={1} max={100} />)
    expect(screen.getByLabelText('Limit').getAttribute('aria-invalid')).toBe('true')
  })
})

describe('what people paste and type (item 18)', () => {
  const type = (text: string) => fireEvent.change(screen.getByLabelText('Limit'), { target: { value: text } })

  it('understands full-width digits, surrounding spaces and thousands separators', () => {
    const onValue = vi.fn()
    render(<Harness initial={null} onValue={onValue} />)
    type('１２')
    expect(onValue).toHaveBeenLastCalledWith(12)
    type(' 5 ')
    expect(onValue).toHaveBeenLastCalledWith(5)
    type('1,000')
    expect(onValue).toHaveBeenLastCalledWith(1000)
    type('１，２３４．５')
    expect(onValue).toHaveBeenLastCalledWith(1234.5)
    expect((screen.getByLabelText('Limit') as HTMLInputElement).value).toBe('1234.5')
  })

  it('refuses scientific notation and says so, leaving the value as it was', () => {
    const onValue = vi.fn()
    render(<Harness initial={7} onValue={onValue} />)
    type('1e3')
    expect(onValue).not.toHaveBeenCalled()
    expect((screen.getByLabelText('Limit') as HTMLInputElement).value).toBe('7')
    expect(screen.getByText(/Scientific notation is not supported/)).toBeTruthy()
    type('8')
    expect(screen.queryByText(/Scientific notation/)).toBeNull()
  })

  it('refuses an ambiguous "1,5" with a message that says what to type', () => {
    render(<Harness initial={7} />)
    type('1,5')
    expect(screen.getByText('Enter digits only, such as 1000 or 1.5')).toBeTruthy()
  })

  it('tells the person when a value is out of range, not only through aria-invalid', () => {
    render(<Harness initial={500} min={1} max={100} />)
    expect(screen.getByLabelText('Limit').getAttribute('aria-invalid')).toBe('true')
    expect(screen.getByText('Allowed range is 1 to 100')).toBeTruthy()
    expect(screen.getByLabelText('Limit').getAttribute('aria-describedby')).toBeTruthy()
  })

  it('names the single bound when there is only one', () => {
    render(<Harness initial={-3} min={0} />)
    expect(screen.getByText('Minimum is 0')).toBeTruthy()
  })

  it('clamps an out-of-range value when the box is left, and says it did', () => {
    const onValue = vi.fn()
    render(<Harness initial={5} min={1} max={100} onValue={onValue} />)
    type('500')
    fireEvent.blur(screen.getByLabelText('Limit'))
    expect(onValue).toHaveBeenLastCalledWith(100)
    expect((screen.getByLabelText('Limit') as HTMLInputElement).value).toBe('100')
    expect(screen.getByText('Out of range, changed to 100')).toBeTruthy()
  })

  it('reports empty, not the previous number, while only a minus sign has been typed', () => {
    const onValue = vi.fn()
    render(<Harness initial={5} onValue={onValue} />)
    type('-')
    expect(onValue).toHaveBeenLastCalledWith(null)
    type('-.')
    expect(onValue).toHaveBeenLastCalledWith(null)
    type('-3')
    expect(onValue).toHaveBeenLastCalledWith(-3)
  })

  it('keeps empty and 0 apart after all of this', () => {
    const onValue = vi.fn()
    render(<Harness initial={5} onValue={onValue} />)
    type('0')
    expect(onValue).toHaveBeenLastCalledWith(0)
    type('')
    expect(onValue).toHaveBeenLastCalledWith(null)
  })
})
