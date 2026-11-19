// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { InheritanceField, SourceTrail, effectiveLevel } from './InheritanceField'

afterEach(cleanup)

const show = (v: number) => (v === 0 ? 'Unlimited' : `${v} GB`)
const editor = ({ value, onChange, id }: { value: number; onChange: (v: number) => void; id: string }) => (
  <input id={id} aria-label="Edit value" value={value} onChange={e => onChange(Number(e.target.value))} />
)
const lv = (global: number | undefined, group: number | undefined, user: number | undefined) => [
  { id: 'global', label: 'Global', value: global },
  { id: 'group', label: 'Group', value: group },
  { id: 'user', label: 'User', value: user },
]

describe('effectiveLevel', () => {
  it('is the deepest level that sets a value', () => {
    expect(effectiveLevel(lv(100, 50, undefined))?.id).toBe('group')
    expect(effectiveLevel(lv(100, undefined, 0))?.id).toBe('user')
    expect(effectiveLevel(lv(100, undefined, undefined))?.id).toBe('global')
    expect(effectiveLevel(lv(undefined, undefined, undefined))).toBeUndefined()
  })
})

describe('SourceTrail', () => {
  it('shows every level in order and marks the one in effect', () => {
    render(<SourceTrail levels={lv(100, 50, undefined)} renderValue={show} />)
    const items = screen.getAllByRole('listitem')
    expect(items.map(i => i.textContent)).toEqual([
      expect.stringContaining('Global'), expect.stringContaining('Group'), expect.stringContaining('User'),
    ])
    expect(items[0].getAttribute('aria-current')).toBeNull()
    expect(items[1].getAttribute('aria-current')).toBe('true')
    expect(within(items[1]).getByText('In effect')).toBeTruthy()
    expect(within(items[1]).getByText('50 GB')).toBeTruthy()
    expect(within(items[2]).getByText('Follows above')).toBeTruthy()
  })

  it('reads an explicit zero as its own value, not as "not set"', () => {
    render(<SourceTrail levels={lv(100, 50, 0)} renderValue={show} />)
    const user = screen.getAllByRole('listitem')[2]
    expect(user.getAttribute('aria-current')).toBe('true')
    expect(within(user).getByText('Unlimited')).toBeTruthy()
    expect(within(user).queryByText('Follows above')).toBeNull()
  })
})

describe('InheritanceField', () => {
  const base = { label: 'Traffic limit', editLevel: 'user', renderValue: show, renderEditor: editor, defaultValue: 10 }

  it('starts in "follow" when the level above is in effect: no editor, no number to mistake for zero', () => {
    render(<InheritanceField {...base} levels={lv(100, 50, undefined)} onChange={() => {}} />)
    expect(screen.getByRole('radio', { name: 'Follow the level above' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.queryByLabelText('Edit value')).toBeNull()
    expect(screen.getByText(/Following the level above, currently 50 GB/)).toBeTruthy()
  })

  it('switching to "set here" starts from the inherited value, not from zero', () => {
    const onChange = vi.fn()
    render(<InheritanceField {...base} levels={lv(100, 50, undefined)} onChange={onChange} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
    expect(onChange).toHaveBeenCalledWith(50)
  })

  it('uses the supplied default when nothing above defines a value', () => {
    const onChange = vi.fn()
    render(<InheritanceField {...base} levels={lv(undefined, undefined, undefined)} onChange={onChange} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
    expect(onChange).toHaveBeenCalledWith(10)
  })

  it('switching back to "follow" reports undefined, which is not 0', () => {
    const onChange = vi.fn()
    render(<InheritanceField {...base} levels={lv(100, 50, 0)} onChange={onChange} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Follow the level above' }))
    expect(onChange).toHaveBeenCalledWith(undefined)
    expect(onChange).not.toHaveBeenCalledWith(0)
  })

  it('shows the editor, holding the explicit zero, when the level sets one', () => {
    render(<InheritanceField {...base} levels={lv(100, 50, 0)} onChange={() => {}} />)
    expect(screen.getByRole('radio', { name: 'Set here' }).getAttribute('aria-checked')).toBe('true')
    expect((screen.getByLabelText('Edit value') as HTMLInputElement).value).toBe('0')
  })

  it('passes edits from the editor through', () => {
    const onChange = vi.fn()
    render(<InheritanceField {...base} levels={lv(100, 50, 20)} onChange={onChange} />)
    fireEvent.change(screen.getByLabelText('Edit value'), { target: { value: '30' } })
    expect(onChange).toHaveBeenCalledWith(30)
  })

  it('names the control by the label and the level it edits', () => {
    render(<InheritanceField {...base} levels={lv(100, 50, undefined)} onChange={() => {}} />)
    expect(screen.getByRole('radiogroup', { name: 'How User gets its value' })).toBeTruthy()
    expect(screen.getByText('Traffic limit')).toBeTruthy()
  })
})
