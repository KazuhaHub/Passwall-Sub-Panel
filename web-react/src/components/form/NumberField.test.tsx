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
const box = () => screen.getByLabelText('Limit') as HTMLInputElement
const type = (text: string) => fireEvent.change(box(), { target: { value: text } })
const leave = () => fireEvent.blur(box())

describe('NumberField', () => {
  it('shows the unit as a suffix and reports numbers as they are typed', () => {
    const onValue = vi.fn()
    render(<Harness initial={5} unit="GB" onValue={onValue} />)
    expect(screen.getByText('GB')).toBeTruthy()
    type('12')
    expect(onValue).toHaveBeenLastCalledWith(12)
  })

  it('accepts a decimal when integers are not required, and refuses one when they are', () => {
    const onValue = vi.fn()
    const { unmount } = render(<Harness initial={null} onValue={onValue} />)
    type('1.5')
    expect(onValue).toHaveBeenLastCalledWith(1.5)
    unmount()
    render(<Harness initial={4} integer />)
    type('1.5')
    expect(screen.getByText('Enter a whole number, such as 1000')).toBeTruthy()
  })

  describe('editing is separate from committing (item 4)', () => {
    it('keeps an emptied box empty while it is being edited, and reports nothing', () => {
      const onValue = vi.fn()
      render(<Harness initial={500} onValue={onValue} />)
      type('50')
      type('5')
      type('')
      expect(box().value).toBe('')
      expect(onValue).toHaveBeenLastCalledWith(5)
      expect(onValue).not.toHaveBeenCalledWith(null)
    })

    it('an empty commit is the explicit "cleared" event when the field allows empty', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} allowEmpty emptyLabel="Follows the group" onValue={onValue} />)
      type('')
      expect(onValue).not.toHaveBeenCalledWith(null)
      leave()
      expect(onValue).toHaveBeenLastCalledWith(null)
      expect(screen.getByText('Follows the group')).toBeTruthy()
    })

    it('an empty commit on a field that needs a number goes back to the number, with a message', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} onValue={onValue} />)
      type('')
      leave()
      expect(box().value).toBe('5')
      expect(screen.getByText('A number is needed here; changed back to 5')).toBeTruthy()
      expect(onValue).not.toHaveBeenCalledWith(null)
    })

    it('Enter commits like leaving the box', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} allowEmpty onValue={onValue} />)
      type('')
      fireEvent.keyDown(box(), { key: 'Enter' })
      expect(onValue).toHaveBeenLastCalledWith(null)
    })

    it('typing "-" or "." over a value does not wipe it from the parent while editing', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} onValue={onValue} />)
      type('-')
      expect(box().value).toBe('-')
      type('.')
      expect(box().value).toBe('.')
      expect(onValue).not.toHaveBeenCalled()
      type('-3')
      expect(onValue).toHaveBeenLastCalledWith(-3)
    })

    it('a half-typed value that is never finished goes back to the real one when the box is left', () => {
      render(<Harness initial={5} />)
      type('-')
      leave()
      expect(box().value).toBe('5')
      expect(screen.getByText('That could not be read; changed back to 5')).toBeTruthy()
    })
  })

  describe('what people paste and type (items 5, 18)', () => {
    it('understands full-width digits, spaces, a leading plus and thousands separators', () => {
      const onValue = vi.fn()
      render(<Harness initial={null} onValue={onValue} />)
      type('１２')
      expect(onValue).toHaveBeenLastCalledWith(12)
      type(' 5 ')
      expect(onValue).toHaveBeenLastCalledWith(5)
      type('+5')
      expect(onValue).toHaveBeenLastCalledWith(5)
      type('1,000')
      expect(onValue).toHaveBeenLastCalledWith(1000)
      type('１，２３４．５')
      expect(onValue).toHaveBeenLastCalledWith(1234.5)
      leave()
      expect(box().value).toBe('1234.5')
    })

    it('accepts "1,000" typed key by key: the "1," on the way is not an error', () => {
      const onValue = vi.fn()
      render(<Harness initial={null} onValue={onValue} />)
      type('1')
      type('1,')
      expect(screen.queryByText(/Enter digits only/)).toBeNull()
      type('1,0')
      type('1,00')
      type('1,000')
      expect(onValue).toHaveBeenLastCalledWith(1000)
    })

    it('does not block keystrokes: unreadable text stays in the box with guidance, and is not reported', () => {
      const onValue = vi.fn()
      render(<Harness initial={7} onValue={onValue} />)
      type('abc')
      expect(box().value).toBe('abc')
      expect(screen.getByText('Enter digits only, such as 1000 or 1.5')).toBeTruthy()
      expect(onValue).not.toHaveBeenCalled()
      type('8')
      expect(screen.queryByText(/Enter digits only/)).toBeNull()
      expect(onValue).toHaveBeenLastCalledWith(8)
    })

    it('says scientific notation is not supported and does not read it as 1000', () => {
      const onValue = vi.fn()
      render(<Harness initial={7} onValue={onValue} />)
      type('1e3')
      expect(onValue).not.toHaveBeenCalled()
      expect(screen.getByText(/Scientific notation is not supported/)).toBeTruthy()
      leave()
      expect(box().value).toBe('7')
    })

    it('does not guess at an ambiguous "1,5": fine while typing (it may become 1,500), refused when left', () => {
      const onValue = vi.fn()
      render(<Harness initial={7} onValue={onValue} />)
      type('1,5')
      expect(onValue).not.toHaveBeenCalled()
      expect(screen.queryByText(/Enter digits only/)).toBeNull()
      leave()
      expect(box().value).toBe('7')
      expect(screen.getByText('That could not be read; changed back to 7')).toBeTruthy()
    })

    it('tells the person when a value is out of range, not only through aria-invalid', () => {
      render(<Harness initial={500} min={1} max={100} />)
      expect(box().getAttribute('aria-invalid')).toBe('true')
      expect(screen.getByText('Allowed range is 1 to 100')).toBeTruthy()
      expect(box().getAttribute('aria-describedby')).toBeTruthy()
    })

    it('names the single bound when there is only one', () => {
      render(<Harness initial={-3} min={0} />)
      expect(screen.getByText('Minimum is 0')).toBeTruthy()
    })

    it('clamps an out-of-range value when the box is left, and says it did', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} min={1} max={100} onValue={onValue} />)
      type('500')
      leave()
      expect(onValue).toHaveBeenLastCalledWith(100)
      expect(box().value).toBe('100')
      expect(screen.getByText('Out of range, changed to 100')).toBeTruthy()
    })
  })

  describe('with an empty-means-follow affordance', () => {
    it('makes the empty state visible and different from zero', () => {
      const { rerender } = render(<NumberField aria-label="Limit" value={null} onChange={() => {}} allowEmpty emptyLabel="Follows the group" />)
      expect(screen.getByText('Follows the group')).toBeTruthy()
      expect(box().value).toBe('')
      rerender(<NumberField aria-label="Limit" value={0} onChange={() => {}} allowEmpty emptyLabel="Follows the group" zeroLabel="Unlimited" />)
      expect(screen.queryByText('Follows the group')).toBeNull()
      expect(box().value).toBe('0')
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

    it('keeps 0 and empty apart', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} allowEmpty onValue={onValue} />)
      type('0')
      expect(onValue).toHaveBeenLastCalledWith(0)
      type('')
      leave()
      expect(onValue).toHaveBeenLastCalledWith(null)
    })
  })

  describe('the message region (item 6)', () => {
    it('is in the page before there is a message, so it is announced when the message arrives', () => {
      const { container } = render(<Harness initial={5} />)
      const live = container.querySelector('[role="status"]')!
      expect(live).not.toBeNull()
      expect(live.textContent).toBe('')
      type('abc')
      expect(container.querySelector('[role="status"]')).toBe(live)
      expect(live.textContent).toContain('Enter digits only')
    })
  })
})
