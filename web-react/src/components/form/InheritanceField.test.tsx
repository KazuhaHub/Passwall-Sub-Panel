// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { InheritanceField, SourceTrail, effectiveLevel } from './InheritanceField'
import { FormValidityProvider } from './FormValidity'
import { NumberField } from './NumberField'
import { SaveBar } from './SaveBar'

afterEach(cleanup)

const show = (v: number) => (v === 0 ? 'Unlimited' : `${v} GB`)
const editor = ({ value, onChange, id, ...aria }: { value: number; onChange: (v: number) => void; id: string; 'aria-labelledby': string; 'aria-describedby'?: string }) => (
  <input id={id} data-testid="editor" aria-labelledby={aria['aria-labelledby']} value={value} onChange={e => onChange(Number(e.target.value))} />
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

describe('SourceTrail on a phone', () => {
  it('stacks the levels on narrow widths and lets a long value wrap instead of truncating the state', () => {
    render(<SourceTrail levels={lv(100, 50, undefined)} renderValue={show} />)
    const list = screen.getByRole('list')
    expect(list.className).toContain('flex-col')
    expect(list.className).toContain('sm:grid')
    const active = screen.getAllByRole('listitem')[1]
    expect(active.querySelector('.truncate')).toBeNull()
  })
})

describe('InheritanceField', () => {
  const base = { label: 'Traffic limit', editLevel: 'user', renderValue: show, renderEditor: editor, defaultValue: 10 }

  it('starts in "follow" when the level above is in effect: no editor, no number to mistake for zero', () => {
    render(<InheritanceField {...base} levels={lv(100, 50, undefined)} onChange={() => {}} />)
    expect(screen.getByRole('radio', { name: 'Follow the level above' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.queryByTestId('editor')).toBeNull()
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
    expect((screen.getByTestId('editor') as HTMLInputElement).value).toBe('0')
  })

  it('passes edits from the editor through', () => {
    const onChange = vi.fn()
    render(<InheritanceField {...base} levels={lv(100, 50, 20)} onChange={onChange} />)
    fireEvent.change(screen.getByTestId('editor'), { target: { value: '30' } })
    expect(onChange).toHaveBeenCalledWith(30)
  })

  it('gives the editor the field label as its accessible name', () => {
    render(<InheritanceField {...base} levels={lv(100, 50, 20)} onChange={() => {}} />)
    expect(screen.getByRole('textbox', { name: 'Traffic limit' })).toBeTruthy()
  })

  it('names the control by the label and the level it edits', () => {
    render(<InheritanceField {...base} levels={lv(100, 50, undefined)} onChange={() => {}} />)
    expect(screen.getByRole('radiogroup', { name: 'Traffic limit: how User gets its value' })).toBeTruthy()
    expect(screen.getByText('Traffic limit')).toBeTruthy()
  })

  describe('the editor is for filling a value; following is the mode control\'s job', () => {
    function Wired() {
      const [user, setUser] = useState<number | undefined>(20)
      return (
        <InheritanceField
          label="Traffic limit" editLevel="user" renderValue={show} defaultValue={10}
          levels={lv(100, 50, user)} onChange={setUser}
          renderEditor={({ id, value, onChange, ...aria }) => <NumberField id={id} {...aria} value={value} onChange={v => { if (v !== null) onChange(v) }} allowEmpty emptyLabel="Follows the group" unit="GB" />}
        />
      )
    }

    it('shows no follow button and no "follows" label inside the editor, even if the NumberField asked for them', () => {
      render(<Wired />)
      expect(screen.queryByRole('button', { name: 'Follow again' })).toBeNull()
      expect(screen.queryByText('Follows the group')).toBeNull()
    })

    it('an emptied editor stays where it is and is invalid until filled, with the way out named', () => {
      render(<Wired />)
      const input = screen.getByRole('textbox', { name: 'Traffic limit' }) as HTMLInputElement
      input.focus()
      fireEvent.change(input, { target: { value: '' } })
      fireEvent.keyDown(input, { key: 'Enter' })
      fireEvent.blur(input)
      expect(screen.getByRole('textbox', { name: 'Traffic limit' })).toBe(input)
      expect(input.getAttribute('aria-invalid')).toBe('true')
      expect(screen.getByText('Fill in a value, or switch to “Follow the level above”')).toBeTruthy()
      // the mode control is how you leave
      fireEvent.click(screen.getByRole('radio', { name: 'Follow the level above' }))
      expect(screen.queryByRole('textbox', { name: 'Traffic limit' })).toBeNull()
    })
  })

  describe('a level above that this editor cannot hold exactly', () => {
    // 1.25 above, a whole-number editor: the starting value has to change to be usable.
    function Wired({ onWrite }: { onWrite: (v: number | undefined) => void }) {
      const [user, setUser] = useState<number | undefined>(undefined)
      return (
        <InheritanceField
          label="Traffic limit" editLevel="user" renderValue={show} defaultValue={10}
          levels={lv(100, 1.25, user)}
          onChange={v => { onWrite(v); setUser(v) }}
          fitInherited={v => Math.round(v)}
          renderEditor={({ id, value, onChange, ...aria }) => <NumberField id={id} {...aria} value={value} onChange={v => { if (v !== null) onChange(v) }} integer min={0} />}
        />
      )
    }
    const editorBox = () => screen.getByRole('textbox', { name: 'Traffic limit' }) as HTMLInputElement

    it('starts from the adjusted value without writing anything, so the form is not dirty', () => {
      const onWrite = vi.fn()
      render(<Wired onWrite={onWrite} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      expect(onWrite).not.toHaveBeenCalled()
      expect(editorBox().value).toBe('1')
      expect(editorBox().getAttribute('aria-invalid')).toBeNull()
    })

    it('says the starting value was adjusted, and that nothing applies until it is edited', () => {
      render(<Wired onWrite={() => {}} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      expect(screen.getByText('The level above is 1.25 GB, which this field cannot hold. Starting from 1 GB instead. Nothing is saved until you edit it.')).toBeTruthy()
    })

    it('leaving the box untouched commits nothing; only an edit writes, and the note goes away', () => {
      const onWrite = vi.fn()
      render(<Wired onWrite={onWrite} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      fireEvent.focus(editorBox())
      fireEvent.blur(editorBox())
      expect(onWrite).not.toHaveBeenCalled()
      fireEvent.change(editorBox(), { target: { value: '3' } })
      expect(onWrite).toHaveBeenCalledWith(3)
      expect(screen.queryByText(/which this field cannot use/)).toBeNull()
    })

    it('going back to follow drops the pending start', () => {
      const onWrite = vi.fn()
      render(<Wired onWrite={onWrite} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      fireEvent.click(screen.getByRole('radio', { name: 'Follow the level above' }))
      expect(screen.queryByRole('textbox', { name: 'Traffic limit' })).toBeNull()
      expect(screen.queryByText(/which this field cannot use/)).toBeNull()
    })

    it('Escape after an edit goes back to "pending, nothing written", not to a stored 1', () => {
      const onWrite = vi.fn()
      render(<Wired onWrite={onWrite} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      fireEvent.change(editorBox(), { target: { value: '9' } })
      expect(onWrite).toHaveBeenLastCalledWith(9)
      fireEvent.keyDown(editorBox(), { key: 'Escape' })
      // the page was told to forget the edit, never to store the fitted start as the person's own value
      expect(onWrite).toHaveBeenLastCalledWith(undefined)
      expect(onWrite).not.toHaveBeenCalledWith(1)
      expect(editorBox().value).toBe('1')
      expect(screen.getByText(/Starting from 1 GB instead/)).toBeTruthy()
    })

    it('typing the same number as the start is still the person\'s own choice and is written', () => {
      const onWrite = vi.fn()
      render(<Wired onWrite={onWrite} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      fireEvent.change(editorBox(), { target: { value: '2' } })
      fireEvent.change(editorBox(), { target: { value: '1' } })
      expect(onWrite).toHaveBeenLastCalledWith(1)
    })

    it('describes the editor by the note, so it is read with the box', () => {
      render(<Wired onWrite={() => {}} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      const note = screen.getByText(/Starting from 1 GB instead/)
      expect(note.id).toBeTruthy()
      expect(editorBox().getAttribute('aria-describedby') ?? '').toContain(note.id)
    })

    it('announces the note when it appears (it sits in a polite live region)', () => {
      render(<Wired onWrite={() => {}} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      expect(screen.getByText(/Starting from 1 GB instead/).closest('[aria-live="polite"]')).not.toBeNull()
    })

    it('the form\'s Discard returns it to "follows above"', () => {
      function Page() {
        const [user, setUser] = useState<number | undefined>(undefined)
        return (
          <FormValidityProvider>
            <InheritanceField
              label="Traffic limit" editLevel="user" renderValue={show} defaultValue={10}
              levels={lv(100, 1.25, user)} onChange={setUser} fitInherited={v => Math.round(v)}
              renderEditor={({ id, value, onChange, ...aria }) => <NumberField id={id} {...aria} value={value} onChange={v => { if (v !== null) onChange(v) }} integer min={0} />}
            />
            <SaveBar dirty onSave={() => {}} onDiscard={() => setUser(undefined)} />
          </FormValidityProvider>
        )
      }
      render(<Page />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      expect(screen.getByRole('textbox', { name: 'Traffic limit' })).toBeTruthy()
      fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }))
      expect(screen.queryByRole('textbox', { name: 'Traffic limit' })).toBeNull()
      expect(screen.getByRole('radio', { name: 'Follow the level above' }).getAttribute('aria-checked')).toBe('true')
    })

    it('a value that arrives from outside forgets the remembered start: Escape then cannot swap it for "follows above"', () => {
      const onWrite = vi.fn()
      function Page({ reload }: { reload?: number }) {
        const [user, setUser] = useState<number | undefined>(undefined)
        const [seen, setSeen] = useState(reload)
        if (seen !== reload) { setSeen(reload); setUser(reload) }
        return (
          <InheritanceField
            label="Traffic limit" editLevel="user" renderValue={show} defaultValue={10}
            levels={lv(100, 1.25, user)} onChange={v => { onWrite(v); setUser(v) }} fitInherited={v => Math.round(v)}
            renderEditor={({ id, value, onChange, ...aria }) => <NumberField id={id} {...aria} value={value} onChange={v => { if (v !== null) onChange(v) }} integer min={0} />}
          />
        )
      }
      const { rerender } = render(<Page />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      fireEvent.change(editorBox(), { target: { value: '9' } })
      // another record is loaded whose own value happens to equal the old fitted start
      rerender(<Page reload={1} />)
      onWrite.mockClear()
      fireEvent.change(editorBox(), { target: { value: '7' } })
      fireEvent.keyDown(editorBox(), { key: 'Escape' })
      expect(onWrite).not.toHaveBeenCalledWith(undefined)
      expect(editorBox().value).toBe('1')
    })

    it('a value the editor can hold is still written at once, as before', () => {
      const onWrite = vi.fn()
      render(<InheritanceField label="Traffic limit" editLevel="user" renderValue={show} defaultValue={10} levels={lv(100, 50, undefined)} onChange={onWrite} fitInherited={v => Math.round(v)} renderEditor={editor} />)
      fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
      expect(onWrite).toHaveBeenCalledWith(50)
    })
  })
})
