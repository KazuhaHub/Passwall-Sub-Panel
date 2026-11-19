// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Field } from './Field'
import { FormSection } from './FormSection'
import { FormValidityProvider } from './FormValidity'
import { NumberField } from './NumberField'
import { SaveBar } from './SaveBar'

afterEach(() => { cleanup(); localStorage.clear() })

const bar = () => screen.getByRole('region', { name: 'You have unsaved changes' })
const save = () => screen.getByRole('button', { name: 'Save' })
const summary = () => within(bar()).getByTitle('Go to the first input that needs fixing')

function Num({ label, initial = 5, ...rest }: { label: string; initial?: number } & Partial<React.ComponentProps<typeof NumberField>>) {
  const [v, setV] = useState<number | null>(initial)
  return <Field label={label}><NumberField value={v} onChange={setV} min={1} max={600} {...rest} /></Field>
}

describe('SaveBar names the fields that block saving', () => {
  it('says which field and what is wrong when one is invalid', () => {
    render(<FormValidityProvider><Num label="Timeout" /><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></FormValidityProvider>)
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    expect(summary().textContent).toBe('Timeout: Allowed range is 1 to 600')
  })

  it('lists every invalid field by label, in page order, when there are several', () => {
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <Num label="Retries" />
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    // reported in the opposite order to the page order
    fireEvent.change(screen.getByLabelText('Retries'), { target: { value: 'abc' } })
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Timeout, Retries')
  })

  it('does not put the required-field asterisk into the name', () => {
    render(
      <FormValidityProvider>
        <Field label="Timeout" required><NumberField value={5} onChange={() => {}} min={10} /></Field>
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    expect(summary().textContent).toBe('Timeout: Minimum is 10')
  })
})

describe('activating the message goes to the first invalid field', () => {
  it('focuses the first invalid field in page order, whichever reported first', () => {
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <Num label="Retries" />
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Retries'), { target: { value: 'abc' } })
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    fireEvent.click(summary())
    expect(document.activeElement).toBe(screen.getByLabelText('Timeout'))
  })

  it('is a real button, so the keyboard reaches it', () => {
    render(<FormValidityProvider><Num label="Timeout" /><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></FormValidityProvider>)
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    expect(summary().tagName).toBe('BUTTON')
    expect((summary() as HTMLButtonElement).type).toBe('button')
  })

  it('pressing Save while blocked also takes the person to the field, and saves nothing', () => {
    const onSave = vi.fn()
    render(<FormValidityProvider><Num label="Timeout" /><SaveBar dirty onSave={onSave} onDiscard={() => {}} /></FormValidityProvider>)
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    fireEvent.click(save())
    expect(onSave).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(screen.getByLabelText('Timeout'))
  })

  it('opens a collapsed advanced section first, without changing what is remembered', () => {
    render(
      <FormValidityProvider>
        <FormSection title="Advanced" advanced sectionKey="t.adv">
          <Num label="MTU" />
        </FormSection>
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    const toggle = screen.getByRole('button', { name: /Advanced/ })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    // the field exists while the section is collapsed, and can already be invalid
    fireEvent.change(screen.getByLabelText('MTU', { selector: 'input' }), { target: { value: 'abc' } })
    fireEvent.click(summary())
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(document.activeElement).toBe(screen.getByLabelText('MTU', { selector: 'input' }))
    expect(localStorage.getItem('psp.formSection.v1.t.adv')).toBeNull()
  })

  it('opens every collapsed section on the way when sections are nested', () => {
    render(
      <FormValidityProvider>
        <FormSection title="Outer" advanced>
          <FormSection title="Inner" advanced>
            <Num label="MTU" />
          </FormSection>
        </FormSection>
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('MTU', { selector: 'input' }), { target: { value: 'abc' } })
    fireEvent.click(summary())
    expect(screen.getByRole('button', { name: /Outer/ }).getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByRole('button', { name: /Inner/ }).getAttribute('aria-expanded')).toBe('true')
  })
})

describe('errors the page passes itself count with the fields that report', () => {
  it('shows and counts both, naming each', () => {
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <SaveBar dirty errors={[{ label: 'Name', message: 'Required' }]} onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    expect(bar().textContent).toContain('2 inputs need fixing: Timeout, Name')
    expect(save().getAttribute('aria-disabled')).toBe('true')
  })

  it('a page error alone blocks and says what it is', () => {
    render(<SaveBar dirty errors={[{ label: 'Name', message: 'Enter a name' }]} onSave={() => {}} onDiscard={() => {}} />)
    expect(bar().textContent).toContain('Name: Enter a name')
    expect(save().getAttribute('aria-disabled')).toBe('true')
  })

  it('goes to a page error when no field reported one', () => {
    const focus = vi.fn()
    render(<SaveBar dirty errors={[{ label: 'Name', message: 'Enter a name', focus }]} onSave={() => {}} onDiscard={() => {}} />)
    fireEvent.click(within(bar()).getByRole('button', { name: /Name/ }))
    expect(focus).toHaveBeenCalledTimes(1)
  })

  it('counts the legacy disabled flag with its message next to the fields instead of hiding it', () => {
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <SaveBar dirty disabled message="Name is missing" onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    expect(bar().textContent).toContain('2 inputs need fixing')
  })
})

describe('a field that leaves the form stops blocking it', () => {
  it('unregisters on unmount, so a hidden conditional field cannot hold the save hostage', () => {
    function Form({ show }: { show: boolean }) {
      return (
        <FormValidityProvider>
          {show && <Num label="Timeout" initial={9999} />}
          <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
        </FormValidityProvider>
      )
    }
    const { rerender } = render(<Form show />)
    expect(save().getAttribute('aria-disabled')).toBe('true')
    rerender(<Form show={false} />)
    expect(save().getAttribute('aria-disabled')).toBeNull()
    expect(bar().textContent).toContain('You have unsaved changes')
  })
})

describe('nested providers', () => {
  it('an invalid field in the inner form blocks only the inner SaveBar', () => {
    render(
      <FormValidityProvider>
        <Num label="Outer value" />
        <div data-testid="outer-bar"><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></div>
        <FormValidityProvider>
          <Num label="Inner value" />
          <div data-testid="inner-bar"><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></div>
        </FormValidityProvider>
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Inner value'), { target: { value: 'abc' } })
    const outer = within(screen.getByTestId('outer-bar')).getByRole('button', { name: 'Save' })
    const inner = within(screen.getByTestId('inner-bar')).getByRole('button', { name: 'Save' })
    expect(inner.getAttribute('aria-disabled')).toBe('true')
    expect(outer.getAttribute('aria-disabled')).toBeNull()
  })

  it('an invalid field in the outer form does not block the inner SaveBar either', () => {
    render(
      <FormValidityProvider>
        <Num label="Outer value" />
        <FormValidityProvider>
          <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
        </FormValidityProvider>
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Outer value'), { target: { value: 'abc' } })
    expect(save().getAttribute('aria-disabled')).toBeNull()
  })
})
