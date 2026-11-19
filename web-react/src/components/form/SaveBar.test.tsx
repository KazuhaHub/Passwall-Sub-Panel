// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SaveBar } from './SaveBar'

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
    expect(screen.getByText('You have unsaved changes')).toBeTruthy()
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
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText('Fix 2 fields to save')).toBeTruthy()
  })

  it('stays visible while a save that cleared the dirty flag is still pending', () => {
    render(<SaveBar dirty={false} pending onSave={() => {}} onDiscard={() => {}} />)
    expect(screen.getByRole('region')).toBeTruthy()
  })
})
