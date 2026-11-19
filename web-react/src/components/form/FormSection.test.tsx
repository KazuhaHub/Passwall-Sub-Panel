// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { FormSection, formSectionKey } from './FormSection'

beforeEach(() => localStorage.clear())
afterEach(cleanup)

describe('FormSection', () => {
  it('is a named region with a title, a description and its fields', () => {
    render(<FormSection title="Basics" description="Name and region"><input aria-label="n" /></FormSection>)
    const region = screen.getByRole('region', { name: 'Basics' })
    expect(region.textContent).toContain('Name and region')
    expect(screen.getByLabelText('n')).toBeTruthy()
  })

  it('collapses an advanced section by default but keeps its fields mounted', () => {
    render(<FormSection title="Network" advanced sectionKey="node.network"><input aria-label="mtu" /></FormSection>)
    const toggle = screen.getByRole('button', { name: /Network/ })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    // still in the DOM so the draft survives, just not visible
    expect(screen.getByLabelText('mtu', { selector: 'input' }).closest('[hidden]')).toBeTruthy()
  })

  it('opens on click and remembers the choice per key', () => {
    const { unmount } = render(<FormSection title="Network" advanced sectionKey="node.network"><input aria-label="mtu" /></FormSection>)
    fireEvent.click(screen.getByRole('button', { name: /Network/ }))
    expect(screen.getByRole('button', { name: /Network/ }).getAttribute('aria-expanded')).toBe('true')
    expect(localStorage.getItem(formSectionKey('node.network'))).toBe('1')
    unmount()
    render(<FormSection title="Network" advanced sectionKey="node.network"><input aria-label="mtu" /></FormSection>)
    expect(screen.getByRole('button', { name: /Network/ }).getAttribute('aria-expanded')).toBe('true')
    cleanup()
    render(<FormSection title="Other" advanced sectionKey="node.other"><input aria-label="x" /></FormSection>)
    expect(screen.getByRole('button', { name: /Other/ }).getAttribute('aria-expanded')).toBe('false')
  })

  it('stays open while it holds an error, whatever was remembered', () => {
    render(<FormSection title="Network" advanced sectionKey="node.network" forceOpen><input aria-label="mtu" /></FormSection>)
    expect(screen.getByRole('button', { name: /Network/ }).getAttribute('aria-expanded')).toBe('true')
  })
})
