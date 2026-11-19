// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Field } from './Field'
import { FormValidityProvider, useFormValidity } from './FormValidity'
import { NumberField } from './NumberField'

afterEach(cleanup)

function Harness(props: { initial: number | null; onValue?: (v: number | null) => void; allow?: (v: number | null) => boolean } & Partial<React.ComponentProps<typeof NumberField>>) {
  const { initial, onValue, allow, ...rest } = props
  const [v, setV] = useState<number | null>(initial)
  return <NumberField aria-label="Limit" value={v} onChange={n => { onValue?.(n); if (!allow || allow(n)) setV(n) }} {...rest} />
}
const box = () => screen.getByLabelText('Limit') as HTMLInputElement
const type = (text: string) => fireEvent.change(box(), { target: { value: text } })
const leave = () => fireEvent.blur(box())

function Blocked() {
  const { issues, blocked } = useFormValidity()
  return <output data-testid="blocked">{blocked ? issues.map(i => i.message).join('|') : 'ok'}</output>
}

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

  describe('editing is separate from committing', () => {
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

    it('an empty box where a number is required is INVALID until filled: it stays empty, with a message', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} onValue={onValue} />)
      type('')
      leave()
      expect(box().value).toBe('')
      expect(box().getAttribute('aria-invalid')).toBe('true')
      expect(screen.getByText('Enter a number, such as 1000')).toBeTruthy()
      expect(onValue).not.toHaveBeenCalledWith(null)
    })

    it('Enter commits like leaving the box', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} allowEmpty onValue={onValue} />)
      type('')
      fireEvent.keyDown(box(), { key: 'Enter' })
      expect(onValue).toHaveBeenLastCalledWith(null)
    })

    it('Escape puts the box back to the current value and takes the problem away', () => {
      render(<Harness initial={5} />)
      type('abc')
      expect(screen.getByText(/Enter digits only/)).toBeTruthy()
      fireEvent.keyDown(box(), { key: 'Escape' })
      expect(box().value).toBe('5')
      expect(box().getAttribute('aria-invalid')).toBeNull()
    })

    it('Escape on an out-of-range draft goes back to the last committed value and clears the invalid state', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} min={1} max={600} onValue={onValue} />)
      type('9999')
      // the parent was told the number as it was typed
      expect(onValue).toHaveBeenLastCalledWith(9999)
      expect(box().getAttribute('aria-invalid')).toBe('true')
      fireEvent.keyDown(box(), { key: 'Escape' })
      expect(box().value).toBe('5')
      expect(box().getAttribute('aria-invalid')).toBeNull()
      // and the parent no longer holds the abandoned number
      expect(onValue).toHaveBeenLastCalledWith(5)
    })

    it('Escape goes back to the value last committed with Enter, not to the one the box started with', () => {
      render(<Harness initial={5} min={1} max={600} />)
      type('20')
      fireEvent.keyDown(box(), { key: 'Enter' })
      type('9999')
      fireEvent.keyDown(box(), { key: 'Escape' })
      expect(box().value).toBe('20')
    })

    it('Escape on an unreadable draft of an allow-empty field with nothing set goes back to empty and tells the parent nothing', () => {
      const onValue = vi.fn()
      render(<Harness initial={null} allowEmpty onValue={onValue} />)
      type('abc')
      fireEvent.keyDown(box(), { key: 'Escape' })
      expect(box().value).toBe('')
      expect(box().getAttribute('aria-invalid')).toBeNull()
      expect(onValue).not.toHaveBeenCalled()
    })

    it('typing "-" or "." over a value does not wipe it from the parent, and is flagged until finished', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} onValue={onValue} />)
      type('-')
      expect(box().value).toBe('-')
      expect(onValue).not.toHaveBeenCalled()
      expect(box().getAttribute('aria-invalid')).toBe('true')
      leave()
      // left unfinished: the text stays and now says so
      expect(box().value).toBe('-')
      expect(screen.getByText('The number is incomplete; finish typing it, such as 1000')).toBeTruthy()
      type('-3')
      expect(onValue).toHaveBeenLastCalledWith(-3)
    })
  })

  describe('what people paste and type', () => {
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

    it('accepts "1,000" typed key by key: the "1," on the way is not announced as an error', () => {
      const onValue = vi.fn()
      render(<Harness initial={null} onValue={onValue} />)
      type('1')
      type('1,')
      expect(screen.queryByText(/Enter digits only/)).toBeNull()
      expect(screen.queryByText('The number is incomplete; finish typing it, such as 1000')).toBeNull()
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

    it('says scientific notation is not supported and does not read it as 1000; leaving the box does not change it', () => {
      const onValue = vi.fn()
      render(<Harness initial={7} onValue={onValue} />)
      type('1e3')
      expect(onValue).not.toHaveBeenCalled()
      expect(screen.getByText(/Scientific notation is not supported/)).toBeTruthy()
      leave()
      expect(box().value).toBe('1e3')
    })

    it('tells the person when a value is out of range, from state, whatever the language', async () => {
      const i18n = (await import('@/test/initI18n')).default
      render(<Harness initial={500} min={1} max={100} />)
      expect(box().getAttribute('aria-invalid')).toBe('true')
      expect(screen.getByText('Allowed range is 1 to 100')).toBeTruthy()
      await i18n.changeLanguage('zh-CN')
      try {
        cleanup()
        render(<Harness initial={500} min={1} max={100} />)
        expect(box().getAttribute('aria-invalid')).toBe('true')
        expect(screen.getByText('取值范围是 1 到 100')).toBeTruthy()
      } finally {
        await i18n.changeLanguage('en-US')
      }
    })

    it('names the single bound when there is only one', () => {
      render(<Harness initial={-3} min={0} />)
      expect(screen.getByText('Minimum is 0')).toBeTruthy()
    })

    it('does NOT clamp behind the person\'s back: 9999 in a 1-600 box stays 9999 and is invalid', () => {
      const onValue = vi.fn()
      render(<Harness initial={5} min={1} max={600} onValue={onValue} />)
      type('9999')
      leave()
      expect(box().value).toBe('9999')
      expect(onValue).not.toHaveBeenCalledWith(600)
      expect(box().getAttribute('aria-invalid')).toBe('true')
      expect(screen.getByText('Allowed range is 1 to 600')).toBeTruthy()
    })
  })

  describe('a parent that refuses the value', () => {
    it('shows the value the parent holds after the commit, with a note, instead of a number that will not be saved', () => {
      render(<Harness initial={5} allow={n => n !== 7} />)
      type('7')
      expect(box().value).toBe('7')
      leave()
      expect(box().value).toBe('5')
      expect(screen.getByText('That value was not accepted and was changed back to 5; enter a different number')).toBeTruthy()
    })

    it('the same for an empty commit that is refused', () => {
      render(<Harness initial={5} allowEmpty allow={n => n !== null} />)
      type('')
      leave()
      expect(box().value).toBe('5')
      expect(screen.getByText('That value was not accepted and was changed back to 5; enter a different number')).toBeTruthy()
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

  describe('the message line (no layout shift, always in the page)', () => {
    it('on its own, keeps one line in the page before there is a message, and the same node after', () => {
      const { container } = render(<Harness initial={5} />)
      const live = container.querySelector('[role="status"]') as HTMLElement
      expect(live).not.toBeNull()
      expect(live.textContent).toBe('')
      expect(live.className).toContain('min-h-4')
      type('abc')
      expect(container.querySelector('[role="status"]')).toBe(live)
      expect(live.textContent).toContain('Enter digits only')
    })

    it('inside a Field, the message goes to the Field\'s reserved line and describes the box', () => {
      const { container } = render(<Field label="Limit" hint="1-600"><NumberField value={5} onChange={() => {}} /></Field>)
      const live = container.querySelector('[aria-live]') as HTMLElement
      expect(live.parentElement!.className).toContain('min-h-4')
      const input = screen.getByLabelText('Limit') as HTMLInputElement
      fireEvent.change(input, { target: { value: 'abc' } })
      expect(live.textContent).toContain('Enter digits only')
      expect(input.getAttribute('aria-describedby')).toBe(live.id)
      // and nothing else on the page carries the message
      expect(container.querySelectorAll('[role="status"]')).toHaveLength(0)
    })
  })

  describe('empty drafts', () => {
    it('with allowEmpty an empty draft is valid: not flagged, not reported, and committed as null on leaving', () => {
      const onValue = vi.fn()
      render(<FormValidityProvider><Harness initial={5} allowEmpty emptyLabel="Follows the group" onValue={onValue} /><Blocked /></FormValidityProvider>)
      type('')
      expect(box().getAttribute('aria-invalid')).toBeNull()
      expect(screen.getByTestId('blocked').textContent).toBe('ok')
      expect(onValue).not.toHaveBeenCalledWith(null)
      leave()
      expect(onValue).toHaveBeenLastCalledWith(null)
      expect(screen.getByTestId('blocked').textContent).toBe('ok')
    })

    it('without allowEmpty an empty draft is invalid and says what to enter', () => {
      render(<FormValidityProvider><Harness initial={5} /><Blocked /></FormValidityProvider>)
      type('')
      expect(box().getAttribute('aria-invalid')).toBe('true')
      expect(screen.getAllByText('Enter a number, such as 1000').length).toBeGreaterThan(0)
      expect(screen.getByTestId('blocked').textContent).toBe('Enter a number, such as 1000')
    })

    it('without allowEmpty but with a range it says the range to enter', () => {
      render(<Harness initial={5} min={1} max={600} />)
      type('')
      expect(screen.getByText('Enter a number from 1 to 600')).toBeTruthy()
    })
  })

  describe('it reports its validity to the form', () => {
    function Status() {
      const { issues, blocked } = useFormValidity()
      return <output data-testid="status">{blocked ? issues.map(i => i.message).join('|') : 'ok'}</output>
    }
    const mount = (initial: number | null, props: Partial<React.ComponentProps<typeof NumberField>> = {}) =>
      render(<FormValidityProvider><Harness initial={initial} {...props} /><Status /></FormValidityProvider>)
    const status = () => screen.getByTestId('status').textContent

    it('is fine while the value is usable', () => {
      mount(5, { min: 1, max: 600 })
      expect(status()).toBe('ok')
    })

    it('blocks while the box holds an out-of-range number, an unreadable one or an empty required one', () => {
      mount(5, { min: 1, max: 600 })
      type('9999')
      expect(status()).toBe('Allowed range is 1 to 600')
      type('abc')
      expect(status()).toContain('Enter digits only')
      type('')
      expect(status()).toBe('Enter a number from 1 to 600')
      type('50')
      expect(status()).toBe('ok')
    })

    it('blocks while a number is half-typed, before the box is left', () => {
      mount(5)
      type('-')
      expect(status()).toBe('The number is incomplete; finish typing it, such as 1000')
    })

    it('stops blocking when the field leaves the form', () => {
      const { rerender } = render(<FormValidityProvider><Harness initial={5} min={10} /><Status /></FormValidityProvider>)
      expect(status()).not.toBe('ok')
      rerender(<FormValidityProvider><Status /></FormValidityProvider>)
      expect(status()).toBe('ok')
    })
  })
})
