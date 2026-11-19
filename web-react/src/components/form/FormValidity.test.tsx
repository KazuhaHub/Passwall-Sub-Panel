// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Field } from './Field'
import { FormSection } from './FormSection'
import { FormValidityProvider, useOnFormDiscard } from './FormValidity'
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

// ---- review round: one page order for every source of problems ----

function Plain({ label, error, id }: { label: string; error?: string; id?: string }) {
  return <Field label={label} error={error}><input id={id} /></Field>
}

describe('fields and page errors are one list, in page order', () => {
  it('a Field error ABOVE an invalid number: named first, focused first', () => {
    render(
      <FormValidityProvider>
        <Plain label="Name" error="Enter a name" />
        <Num label="Timeout" />
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Name, Timeout')
    fireEvent.click(summary())
    expect(document.activeElement).toBe(screen.getByLabelText('Name'))
  })

  it('a Field error BELOW an invalid number: the number is first', () => {
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <Plain label="Name" error="Enter a name" />
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Timeout, Name')
    fireEvent.click(summary())
    expect(document.activeElement).toBe(screen.getByLabelText('Timeout'))
  })

  it('a field error clears when the page clears it, and unblocks saving', () => {
    const { rerender } = render(
      <FormValidityProvider><Plain label="Name" error="Enter a name" /><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></FormValidityProvider>,
    )
    expect(save().getAttribute('aria-disabled')).toBe('true')
    rerender(<FormValidityProvider><Plain label="Name" /><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></FormValidityProvider>)
    expect(save().getAttribute('aria-disabled')).toBeNull()
  })

  it('SaveBar errors that name an element by id sort with the fields, above or below', () => {
    const errors = [{ label: 'Name', message: 'Enter a name', id: 'page-name' }]
    const { unmount } = render(
      <FormValidityProvider>
        <input id="page-name" aria-label="Name" />
        <Num label="Timeout" />
        <SaveBar dirty errors={errors} onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Name, Timeout')
    fireEvent.click(summary())
    expect(document.activeElement).toBe(document.getElementById('page-name'))
    unmount()
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <input id="page-name" aria-label="Name" />
        <SaveBar dirty errors={errors} onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Timeout, Name')
    fireEvent.click(summary())
    expect(document.activeElement).toBe(screen.getByLabelText('Timeout'))
  })

  it('an error with no element sorts last and is still named', () => {
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <SaveBar dirty errors={[{ label: 'Whole form', message: 'Conflict' }]} onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Timeout, Whole form')
  })

  it('opens the collapsed section of a page error that names an element in it', () => {
    render(
      <FormValidityProvider>
        <FormSection title="Advanced" advanced>
          <input id="deep" aria-label="MTU" />
        </FormSection>
        <SaveBar dirty errors={[{ label: 'MTU', message: 'Too big', id: 'deep' }]} onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.click(within(bar()).getByTitle('Go to the first input that needs fixing'))
    expect(screen.getByRole('button', { name: /Advanced/ }).getAttribute('aria-expanded')).toBe('true')
    expect(document.activeElement).toBe(document.getElementById('deep'))
  })
})

describe('focus has to land, or the next problem is tried', () => {
  it('skips a problem whose element cannot take focus', () => {
    render(
      <FormValidityProvider>
        <input id="stuck" aria-label="Stuck" disabled />
        <Num label="Timeout" />
        <SaveBar dirty errors={[{ label: 'Stuck', message: 'Bad', id: 'stuck' }]} onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    fireEvent.click(summary())
    expect(document.activeElement).toBe(screen.getByLabelText('Timeout'))
  })
})

describe('a disabled field cannot be fixed, so it never blocks saving', () => {
  it('NumberField', () => {
    render(<FormValidityProvider><Num label="Timeout" initial={9999} disabled /><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></FormValidityProvider>)
    expect(save().getAttribute('aria-disabled')).toBeNull()
  })

  it('stops blocking when an invalid field becomes disabled, and blocks again when it is enabled', () => {
    function Form({ disabled }: { disabled: boolean }) {
      return <FormValidityProvider><Num label="Timeout" initial={9999} disabled={disabled} /><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></FormValidityProvider>
    }
    const { rerender } = render(<Form disabled={false} />)
    expect(save().getAttribute('aria-disabled')).toBe('true')
    rerender(<Form disabled />)
    expect(save().getAttribute('aria-disabled')).toBeNull()
    rerender(<Form disabled={false} />)
    expect(save().getAttribute('aria-disabled')).toBe('true')
  })
})

describe('the status line speaks the blocked state and its count, not every wording change', () => {
  it('does not change while the count stays the same, changes when it does', () => {
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <Num label="Retries" />
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    const sr = () => document.querySelector('p[role="status"].sr-only') as HTMLElement
    expect(sr().textContent).toBe('You have unsaved changes')
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    const one = sr().textContent
    expect(one).not.toBe('You have unsaved changes')
    // a different message for the same single problem: nothing new to announce
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    expect(sr().textContent).toBe(one)
    fireEvent.change(screen.getByLabelText('Retries'), { target: { value: 'abc' } })
    expect(sr().textContent).not.toBe(one)
  })
})

describe('two fields with the same label are told apart', () => {
  it('by the title of the section they are in', () => {
    render(
      <FormValidityProvider>
        <FormSection title="Inbound"><Num label="Port" /></FormSection>
        <FormSection title="Outbound"><Num label="Port" /></FormSection>
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    const ports = screen.getAllByLabelText('Port')
    fireEvent.change(ports[0], { target: { value: 'abc' } })
    fireEvent.change(ports[1], { target: { value: 'abc' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Inbound › Port, Outbound › Port')
  })

  it('by position when there is no section to name', () => {
    render(
      <FormValidityProvider>
        <Num label="Port" />
        <Num label="Port" />
        <SaveBar dirty onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    const ports = screen.getAllByLabelText('Port')
    fireEvent.change(ports[0], { target: { value: 'abc' } })
    fireEvent.change(ports[1], { target: { value: 'abc' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Port (#1), Port (#2)')
  })
})

describe('disabled + message next to field problems', () => {
  it('the message is named in the list as one of the problems', () => {
    render(
      <FormValidityProvider>
        <Num label="Timeout" />
        <SaveBar dirty disabled message="Name is missing" onSave={() => {}} onDiscard={() => {}} />
      </FormValidityProvider>,
    )
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    expect(summary().textContent).toBe('2 inputs need fixing: Timeout, Name is missing')
  })
})

describe('a discard reaches the fields that keep state of their own', () => {
  it('notifies subscribers of the provider', () => {
    const seen = vi.fn()
    function Listener() { useOnFormDiscard(seen); return null }
    render(<FormValidityProvider><Listener /><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></FormValidityProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
    expect(seen).toHaveBeenCalledTimes(1)
  })
})
