// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FormValidityProvider } from './FormValidity'
import { NumberField } from './NumberField'
import { SaveBar } from './SaveBar'
import { TagsField } from './TagsField'

afterEach(cleanup)

describe('SaveBar', () => {
  it('renders nothing while the form is clean', () => {
    render(<SaveBar dirty={false} onSave={() => {}} onDiscard={() => {}} />)
    expect(screen.queryByRole('region')).toBeNull()
  })

  it('says there are unsaved changes and offers save and discard', () => {
    const onSave = vi.fn()
    const onDiscard = vi.fn()
    render(<SaveBar dirty onSave={onSave} onDiscard={onDiscard} />)
    expect(within(screen.getByRole('region')).getByText('You have unsaved changes')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onDiscard).toHaveBeenCalledTimes(1)
  })

  it('is busy while saving: no second click, and the label says so', () => {
    const onSave = vi.fn()
    render(<SaveBar dirty pending onSave={onSave} onDiscard={() => {}} />)
    const save = screen.getByRole('button', { name: /Saving/ })
    expect((save as HTMLButtonElement).disabled).toBe(true)
    expect(save.getAttribute('aria-busy')).toBe('true')
    expect((screen.getByRole('button', { name: 'Discard changes' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('disables save while the form is invalid and says why', () => {
    render(<SaveBar dirty disabled message="Fix 2 fields to save" onSave={() => {}} onDiscard={() => {}} />)
    const save = screen.getByRole('button', { name: 'Save' })
    expect(save.getAttribute('aria-disabled')).toBe('true')
    expect(screen.getByText('Fix 2 fields to save')).toBeTruthy()
  })

  it('stays visible while a save that cleared the dirty flag is still pending', () => {
    render(<SaveBar dirty={false} pending onSave={() => {}} onDiscard={() => {}} />)
    expect(screen.getByRole('region')).toBeTruthy()
  })
})

describe('SaveBar live announcement', () => {
  it('keeps a status region in the page so its appearance is announced, empty while clean', () => {
    const { rerender } = render(<SaveBar dirty={false} onSave={() => {}} onDiscard={() => {}} />)
    const status = screen.getByRole('status')
    expect(status.textContent).toBe('')
    rerender(<SaveBar dirty onSave={() => {}} onDiscard={() => {}} />)
    // the same node, with new text: screen readers announce a change in an existing live region
    expect(screen.getByRole('status')).toBe(status)
    expect(status.textContent).toBe('You have unsaved changes')
  })

  it('says it is saving while pending', () => {
    render(<SaveBar dirty pending onSave={() => {}} onDiscard={() => {}} />)
    expect(screen.getByRole('status').textContent).toBe('Saving')
  })
})

describe('SaveBar and the form validity (item 6)', () => {
  function Form({ onSave, tags }: { onSave: () => void; tags?: boolean }) {
    const [v, setV] = useState<number | null>(5)
    const [t, setT] = useState<string[]>([])
    return (
      <FormValidityProvider>
        <NumberField aria-label="Timeout" value={v} onChange={setV} min={1} max={600} />
        {tags && <TagsField aria-label="Tags" required value={t} onChange={setT} />}
        <SaveBar dirty onSave={onSave} onDiscard={() => {}} />
      </FormValidityProvider>
    )
  }
  const save = () => screen.getByRole('button', { name: 'Save' })

  it('saves with one click while the form is fine', () => {
    const onSave = vi.fn()
    render(<Form onSave={onSave} />)
    expect(save().getAttribute('aria-disabled')).toBeNull()
    fireEvent.click(save())
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('cannot save while a field is invalid, says why, and still can once it is fixed (no click is lost, nothing is stored)', () => {
    const onSave = vi.fn()
    render(<Form onSave={onSave} />)
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '9999' } })
    expect(save().getAttribute('aria-disabled')).toBe('true')
    expect(within(screen.getByRole('region')).getByText('Timeout: Allowed range is 1 to 600')).toBeTruthy()
    // the press that would have stored a clamped 600
    fireEvent.blur(screen.getByLabelText('Timeout'))
    fireEvent.click(save())
    expect(onSave).not.toHaveBeenCalled()
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: '600' } })
    expect(save().getAttribute('aria-disabled')).toBeNull()
    fireEvent.click(save())
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('describes the disabled button by the reason, and keeps it focusable', () => {
    render(<Form onSave={() => {}} />)
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    const reason = document.getElementById(save().getAttribute('aria-describedby')!)!
    expect(reason.textContent).toContain('Enter digits only')
    expect((save() as HTMLButtonElement).disabled).toBe(false)
  })

  it('counts the problems when there are several', () => {
    render(<Form onSave={() => {}} tags />)
    fireEvent.change(screen.getByLabelText('Timeout'), { target: { value: 'abc' } })
    expect(within(screen.getByRole('region')).getByText('2 inputs need fixing: Timeout, Tags')).toBeTruthy()
  })

  it('keeps the bar the same height when its message changes, so the Save button does not move', () => {
    render(<Form onSave={() => {}} />)
    expect(screen.getByRole('region').className).toContain('min-h-14')
  })
})
