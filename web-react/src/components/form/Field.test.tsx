// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { Input } from '@/components/ui/input'
import { Field } from './Field'

afterEach(cleanup)

describe('Field', () => {
  it('puts the label above the control and ties them together', () => {
    render(<Field label="Display name"><Input /></Field>)
    const input = screen.getByLabelText('Display name')
    expect(input.tagName).toBe('INPUT')
    const label = screen.getByText('Display name')
    // label precedes the control in document order: label above, never floating
    expect(label.compareDocumentPosition(input) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('describes the control by the hint', () => {
    render(<Field label="Port" hint="1-65535"><Input /></Field>)
    const input = screen.getByLabelText('Port')
    const hint = screen.getByText('1-65535')
    expect(input.getAttribute('aria-describedby')).toBe(hint.id)
    expect(input.getAttribute('aria-invalid')).toBeNull()
  })

  it('shows the error INSTEAD of the hint and marks the control invalid', () => {
    render(<Field label="Port" hint="1-65535" error="Enter a port between 1 and 65535"><Input /></Field>)
    const input = screen.getByLabelText('Port')
    expect(screen.queryByText('1-65535')).toBeNull()
    const err = screen.getByText('Enter a port between 1 and 65535')
    expect(input.getAttribute('aria-describedby')).toBe(err.id)
    expect(input.getAttribute('aria-invalid')).toBe('true')
  })

  it('marks required fields for sight and for assistive tech', () => {
    render(<Field label="Host" required><Input /></Field>)
    const input = screen.getByLabelText(/Host/)
    expect(input.getAttribute('aria-required')).toBe('true')
    expect(screen.getByText('*').getAttribute('aria-hidden')).toBe('true')
  })

  it('hands the wiring to a render function for controls that are not plain elements', () => {
    render(
      <Field label="Tags" hint="Comma separated">
        {p => <div role="group" id={p.id} aria-describedby={p['aria-describedby']} data-testid="custom" />}
      </Field>,
    )
    const el = screen.getByTestId('custom')
    expect(el.id).toBeTruthy()
    expect(el.getAttribute('aria-describedby')).toBe(screen.getByText('Comma separated').id)
  })

  it('keeps an id the control already has', () => {
    render(<Field label="Name"><Input id="mine" /></Field>)
    expect(screen.getByLabelText('Name').id).toBe('mine')
  })
})
