// @vitest-environment jsdom
import '@/test/initI18n'
import { useState } from 'react'
import { act, cleanup, fireEvent, render, screen, within, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderInRouter } from '@/test/routerHarness'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SaveBar } from '@/components/form/SaveBar'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { DetailDialog } from './DetailDialog'
import { FormDialog } from './FormDialog'
import { useRecordNav } from './useRecordNav'

const realMatchMedia = window.matchMedia
afterEach(() => { cleanup(); window.matchMedia = realMatchMedia })

/** matchMedia where only the phone query matches, so useIsMobile answers true. */
function phone(on: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: on && query.includes('max-width'),
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  })) as unknown as typeof window.matchMedia
}

describe('DetailDialog', () => {
  it('renders nothing while closed', () => {
    render(<DetailDialog open onClose={() => {}} title="T" />)
    cleanup()
    render(<DetailDialog open={false} onClose={() => {}} title="T" />)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('titles the dialog and keeps the header, a scrolling body and the footer apart', () => {
    render(
      <DetailDialog open onClose={() => {}} title="Record 7" status={<span>Online</span>} actions={<button>Edit</button>} footer={<button>Done</button>}>
        <p>Body text</p>
      </DetailDialog>,
    )
    const dialog = screen.getByRole('dialog', { name: 'Record 7' })
    expect(dialog.textContent).toContain('Online')
    const body = dialog.querySelector('[data-slot="detail-dialog-body"]') as HTMLElement
    expect(body.className).toContain('overflow-y-auto')
    expect(body.textContent).toBe('Body text')
    // The footer is outside the scrolling body, so it stays in view while the body scrolls.
    const footer = dialog.querySelector('[data-slot="detail-dialog-footer"]') as HTMLElement
    expect(footer.contains(body)).toBe(false)
    expect(within(footer).getByRole('button', { name: 'Done' })).toBeTruthy()
    expect(within(dialog.querySelector('header')!).getByRole('button', { name: 'Edit' })).toBeTruthy()
    expect(dialog.className).toContain('max-h-[85vh]')
  })

  it.each([['sm', '28rem'], ['md', '36rem'], ['lg', '48rem'], ['xl', '64rem']] as const)('size %s is %s wide on a desktop', (size, rem) => {
    render(<DetailDialog open onClose={() => {}} title="T" size={size}>x</DetailDialog>)
    const dialog = screen.getByRole('dialog')
    expect(dialog.dataset.size).toBe(size)
    expect(dialog.className).toContain(`max-w-[${rem}]`)
  })

  it('is a centred dialog on a desktop and a bottom sheet with a grab bar on a phone', () => {
    phone(false)
    render(<DetailDialog open onClose={() => {}} title="T">x</DetailDialog>)
    let dialog = screen.getByRole('dialog')
    expect(dialog.dataset.layout).toBe('dialog')
    expect(dialog.className).toContain('top-1/2')
    expect(dialog.querySelector('div[aria-hidden]')).toBeNull()
    cleanup()

    phone(true)
    render(<DetailDialog open onClose={() => {}} title="T">x</DetailDialog>)
    dialog = screen.getByRole('dialog')
    expect(dialog.dataset.layout).toBe('sheet')
    expect(dialog.className).toContain('bottom-0')
    expect(dialog.className).toContain('max-h-[92dvh]')
    expect(dialog.className).not.toContain('top-1/2')
    // The grab bar is decoration; the close button is the affordance that works.
    expect(dialog.querySelector('div[aria-hidden]')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Close details' })).toBeTruthy()
  })

  it('closes with its button and on Escape', async () => {
    const onClose = vi.fn()
    render(<DetailDialog open onClose={onClose} title="T">x</DetailDialog>)
    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    expect(onClose).toHaveBeenCalledTimes(1)
    await act(async () => { fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' }) })
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('opens from the URL and closes through Back when a row opened it', async () => {
    function Page() {
      const sel = useUrlSelection('row')
      return (
        <div>
          <button onClick={() => sel.select('7')}>open row</button>
          <DetailDialog open={sel.isOpen} onClose={sel.clear} title={`Row ${sel.selected}`}>x</DetailDialog>
        </div>
      )
    }
    const { router } = renderInRouter(<Page />, ['/list'])
    fireEvent.click(screen.getByRole('button', { name: 'open row' }))
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole('dialog', { name: 'Row 7' })).toBeTruthy()
    await act(async () => { await router.navigate(-1) })
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('names the previous and next buttons, disables them at the ends', () => {
    const onNext = vi.fn()
    const { rerender } = render(<DetailDialog open onClose={() => {}} title="T" nav={{ onNext }}>x</DetailDialog>)
    const prev = screen.getByRole('button', { name: 'Previous record' }) as HTMLButtonElement
    const next = screen.getByRole('button', { name: 'Next record' }) as HTMLButtonElement
    expect(prev.disabled).toBe(true)
    expect(next.disabled).toBe(false)
    expect(screen.getByRole('dialog').textContent).not.toMatch(/\d of \d/)
    fireEvent.click(next)
    expect(onNext).toHaveBeenCalledTimes(1)
    rerender(<DetailDialog open onClose={() => {}} title="T" nav={{ onPrev: () => {} }}>x</DetailDialog>)
    expect((screen.getByRole('button', { name: 'Next record' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Previous record' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('has no previous / next buttons without nav', () => {
    render(<DetailDialog open onClose={() => {}} title="T">x</DetailDialog>)
    expect(screen.queryByRole('button', { name: 'Previous record' })).toBeNull()
  })

  it('wraps body and footer in one form when onSubmit is given', () => {
    const onSubmit = vi.fn((e: { preventDefault: () => void }) => e.preventDefault())
    render(<DetailDialog open onClose={() => {}} title="T" onSubmit={onSubmit} footer={<button type="submit">Save</button>}><input aria-label="name" /></DetailDialog>)
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSubmit).toHaveBeenCalled()
  })
})

describe('stacked dialogs', () => {
  function Stack({ onLowerClose }: { onLowerClose: () => void }) {
    const [upper, setUpper] = useState(false)
    return (
      <DetailDialog open onClose={onLowerClose} title="Lower">
        <button onClick={() => setUpper(true)}>open upper</button>
        <Dialog open={upper} onOpenChange={setUpper}>
          <DialogContent><DialogTitle>Upper</DialogTitle><button>inside upper</button></DialogContent>
        </Dialog>
      </DetailDialog>
    )
  }

  it('Escape closes only the top dialog, and focus returns to what opened it', async () => {
    const onLowerClose = vi.fn()
    render(<Stack onLowerClose={onLowerClose} />)
    const opener = screen.getByRole('button', { name: 'open upper' })
    opener.focus()
    fireEvent.click(opener)
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole('dialog', { name: 'Upper' })).toBeTruthy()
    await act(async () => { fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' }) })
    await act(async () => { await new Promise(r => setTimeout(r, 50)) })
    expect(screen.queryByRole('dialog', { name: 'Upper' })).toBeNull()
    expect(onLowerClose).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog', { name: 'Lower' })).toBeTruthy()
    await waitFor(() => expect(document.activeElement).toBe(opener))
  })
})

describe('save bar placement', () => {
  const bar = <FormValidityProvider><SaveBar dirty onSave={() => {}} onDiscard={() => {}} /></FormValidityProvider>

  it('goes into the footer of the dialog that holds it', () => {
    render(<DetailDialog open onClose={() => {}} title="T">{bar}</DetailDialog>)
    const dialog = screen.getByRole('dialog')
    expect(dialog.querySelector('[data-slot="detail-dialog-footer"] [data-slot="save-bar"]')).toBeTruthy()
    expect(dialog.querySelector('[data-slot="detail-dialog-body"] [data-slot="save-bar"]')).toBeNull()
  })

  it('stays in a plain dialog opened from it, and does not leak into the outer footer', () => {
    render(
      <DetailDialog open onClose={() => {}} title="Outer">
        <Dialog open><DialogContent><DialogTitle>Inner</DialogTitle>{bar}</DialogContent></Dialog>
      </DetailDialog>,
    )
    const outer = document.querySelector('[data-slot="detail-dialog"]') as HTMLElement
    const inner = screen.getByRole('dialog', { name: 'Inner' })
    expect(outer.querySelector('[data-slot="detail-dialog-footer"] [data-slot="save-bar"]')).toBeNull()
    expect(inner.querySelector('[data-slot="save-bar"]')).toBeTruthy()
  })
})

describe('FormDialog', () => {
  it('submits with Cancel and the submit button in the footer, and refuses to close while saving', () => {
    const onSubmit = vi.fn()
    const onClose = vi.fn()
    const { rerender } = render(<FormDialog title="New" dirty={false} pending={false} submitLabel="Create" onSubmit={onSubmit} onClose={onClose}><input aria-label="name" /></FormDialog>)
    const dialog = screen.getByRole('dialog', { name: 'New' })
    const footer = dialog.querySelector('[data-slot="detail-dialog-footer"]') as HTMLElement
    fireEvent.click(within(footer).getByRole('button', { name: 'Create' }))
    expect(onSubmit).toHaveBeenCalledTimes(1)
    fireEvent.click(within(footer).getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalledTimes(1)
    rerender(<FormDialog title="New" dirty={false} pending submitLabel="Create" onSubmit={onSubmit} onClose={onClose}><input aria-label="name" /></FormDialog>)
    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

describe('useRecordNav', () => {
  function Probe({ keys, current, select }: { keys: number[]; current: number | null; select: (k: number) => void }) {
    const nav = useRecordNav(keys, current, select)
    return <div>{nav ? `prev:${nav.onPrev ? 'y' : 'n'} next:${nav.onNext ? 'y' : 'n'}` : 'none'}</div>
  }
  it('steps through the loaded keys in order and ends at the first and last', () => {
    const select = vi.fn()
    const { container, rerender } = render(<Probe keys={[5, 9, 2]} current={9} select={select} />)
    expect(container.textContent).toBe('prev:y next:y')
    rerender(<Probe keys={[5, 9, 2]} current={5} select={select} />)
    expect(container.textContent).toBe('prev:n next:y')
    rerender(<Probe keys={[5, 9, 2]} current={2} select={select} />)
    expect(container.textContent).toBe('prev:y next:n')
  })
  it('offers nothing when the open record is not among the rows', () => {
    const { container } = render(<Probe keys={[1, 2]} current={7} select={() => {}} />)
    expect(container.textContent).toBe('none')
  })
})
