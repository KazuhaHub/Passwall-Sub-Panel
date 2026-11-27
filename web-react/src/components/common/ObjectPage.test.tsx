// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, Link, RouterProvider, useLocation } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import '@/test/initI18n'
import ConfirmHost from '@/components/ConfirmHost'
import { ObjectPage, objectErrorKind } from './ObjectPage'
import { ObjectTabs, useObjectLeaveGuard, type ObjectTab } from './ObjectTabs'
import { useBackTarget, useObjectLink } from './objectReturn'

afterEach(cleanup)

const back = { to: '/things', label: 'Things' }
const at = (error: unknown) => objectErrorKind(error)
const Where = () => { const l = useLocation(); return <p data-testid="where">{l.pathname + l.search}</p> }
function mount(ui: React.ReactNode, entries = ['/things/1']) {
  const router = createMemoryRouter([
    { path: '/things', element: <><p>the list</p><Where /></> },
    { path: '/things/:id', element: <>{ui}<Where /></> },
  ], { initialEntries: entries, initialIndex: entries.length - 1 })
  return { router, ...render(<><ConfirmHost /><RouterProvider router={router} /></>) }
}

describe('the frame', () => {
  it('pads itself, names the object as the one h1, and puts the back link above it', () => {
    mount(<ObjectPage back={back} title="alice" subtitle="Alice A." status={<span>Active</span>}>body</ObjectPage>)
    const page = document.querySelector('[data-slot="object-page"]')!
    expect(page.className).toContain('p-4')
    expect(page.className).toContain('sm:p-6')
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('alice')
    expect(screen.getByRole('link', { name: 'Things' }).getAttribute('href')).toBe('/things')
    expect(screen.getByText('Alice A.')).toBeTruthy()
    expect(screen.getByText('body')).toBeTruthy()
  })

  it('draws an action area only when there is something in it, and puts More last', () => {
    const { unmount } = mount(<ObjectPage back={back} title="x" />)
    expect(document.querySelector('[data-slot="object-page-actions"]')).toBeNull()
    unmount()
    mount(<ObjectPage back={back} title="x" secondary={<button>Edit</button>} more={[{ id: 'a', label: 'Archive', onSelect: () => {} }]} />)
    const actions = document.querySelector('[data-slot="object-page-actions"]')!
    expect(actions.firstElementChild?.textContent).toBe('Edit')
    expect(actions.lastElementChild?.getAttribute('aria-label') ?? actions.lastElementChild?.textContent).toMatch(/More|更多/)
  })

  it('shows every way a load can end inside the frame, with the back link still there', () => {
    const retry = vi.fn()
    const cases = [
      [{ kind: 'loading' as const }, () => document.querySelector('[data-slot="object-loading"]')],
      [{ kind: 'not_found' as const, title: 'No such thing' }, () => screen.getByText('No such thing')],
      [{ kind: 'forbidden' as const, title: 'Not yours' }, () => screen.getByText('Not yours')],
      [{ kind: 'error' as const, reason: new Error('boom'), retry }, () => screen.getByRole('alert')],
    ] as const
    for (const [state, find] of cases) {
      const { unmount } = mount(<ObjectPage back={back} state={state} />)
      expect(find()).toBeTruthy()
      expect(screen.getAllByRole('link', { name: 'Things' }).length).toBeGreaterThan(0)
      expect(document.querySelector('[data-slot="object-page-header"]')).toBeNull()
      unmount()
    }
  })

  it('reads a status code as what it means', () => {
    expect(at({ response: { status: 404 } })).toBe('not_found')
    expect(at({ response: { status: 403 } })).toBe('forbidden')
    expect(at({ response: { status: 500 } })).toBe('error')
    expect(at(new Error('offline'))).toBe('error')
    expect(at(null)).toBe('error')
  })
})

describe('tabs', () => {
  const tabs: ObjectTab<'a' | 'b'>[] = [{ id: 'a', label: 'Alpha' }, { id: 'b', label: 'Beta', dirty: true, width: 'full' }]
  const Panel = ({ name }: { name: string }) => <input aria-label={name} defaultValue="" />

  it('binds the selection to ?tab=, leaves the first tab out of the address, and keeps a visited tab mounted', async () => {
    mount(<ObjectTabs tabs={tabs} unsavedLabel="Unsaved" panels={{ a: <Panel name="in-a" />, b: <Panel name="in-b" /> }} />)
    expect(screen.getByTestId('where').textContent).toBe('/things/1')
    // Not built until visited.
    expect(screen.queryByLabelText('in-b', { selector: 'input' })).toBeNull()
    fireEvent.change(screen.getByLabelText('in-a'), { target: { value: 'draft' } })
    fireEvent.click(screen.getByRole('tab', { name: /Beta/ }))
    await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('/things/1?tab=b'))
    fireEvent.click(screen.getByRole('tab', { name: /Alpha/ }))
    await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('/things/1'))
    // The draft in the first tab survived the visit to the second.
    expect((screen.getByLabelText('in-a') as HTMLInputElement).value).toBe('draft')
    expect(screen.getByRole('img', { name: 'Unsaved' })).toBeTruthy()
  })

  it('opens on the tab the address names, and falls back for one that does not exist', () => {
    const { unmount } = mount(<ObjectTabs tabs={tabs} unsavedLabel="Unsaved" panels={{ a: 'A body', b: 'B body' }} />, ['/things/1?tab=b'])
    expect(screen.getByRole('tab', { name: /Beta/, selected: true })).toBeTruthy()
    unmount()
    mount(<ObjectTabs tabs={tabs} unsavedLabel="Unsaved" panels={{ a: 'A body', b: 'B body' }} />, ['/things/1?tab=zzz'])
    expect(screen.getByRole('tab', { name: /Alpha/, selected: true })).toBeTruthy()
  })

  it('keeps a form tab to a readable column and lets a full tab use the width', () => {
    mount(<ObjectTabs tabs={tabs} unsavedLabel="Unsaved" panels={{ a: 'A body', b: 'B body' }} />, ['/things/1?tab=b'])
    expect(screen.getByText('B body').className).not.toContain('max-w-3xl')
    fireEvent.click(screen.getByRole('tab', { name: /Alpha/ }))
    expect(screen.getByText('A body').className).toContain('max-w-3xl')
  })
})

describe('leaving', () => {
  function Guarded({ dirty, bypass }: { dirty: boolean; bypass?: { current: boolean } }) {
    useObjectLeaveGuard({ dirty, copy: { title: 'Discard?', message: 'Alpha has unsaved changes', confirmText: 'Discard', cancelText: 'Keep' }, bypass })
    return <><Link to="/things">out</Link><Link to="?tab=b">tab</Link></>
  }

  it('asks once when the page is left with edits, and not for a change of tab', async () => {
    const { router } = mount(<Guarded dirty />)
    fireEvent.click(screen.getByRole('link', { name: 'tab' }))
    await waitFor(() => expect(router.state.location.search).toBe('?tab=b'))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    fireEvent.click(screen.getByRole('link', { name: 'out' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog.textContent).toContain('Alpha has unsaved changes')
    fireEvent.click(screen.getByRole('button', { name: 'Keep' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(router.state.location.pathname).toBe('/things/1')
  })

  it('does not ask when nothing is edited, or when the exit makes the edits moot', async () => {
    const { router, unmount } = mount(<Guarded dirty={false} />)
    fireEvent.click(screen.getByRole('link', { name: 'out' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/things'))
    unmount()
    const again = mount(<Guarded dirty bypass={{ current: true }} />)
    fireEvent.click(screen.getByRole('link', { name: 'out' }))
    await waitFor(() => expect(again.router.state.location.pathname).toBe('/things'))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })
})

describe('returning to the list as it was left', () => {
  function ListSide() {
    const link = useObjectLink()
    return <Link {...link('/things/9')}>open</Link>
  }
  function Back() { return <a href={useBackTarget('/things')}>back</a> }
  const mountBoth = (entries: Parameters<typeof createMemoryRouter>[1] extends infer O ? O : never) => {
    const router = createMemoryRouter([
      { path: '/things', element: <ListSide /> },
      { path: '/things/:id', element: <Back /> },
    ], entries as never)
    return render(<RouterProvider router={router} />)
  }

  it('carries the list address, without the drawers over it', async () => {
    mountBoth({ initialEntries: ['/things?page=2&q=a&risk=5'] })
    fireEvent.click(screen.getByRole('link', { name: 'open' }))
    await waitFor(() => expect(screen.getByText('back').getAttribute('href')).toBe('/things?page=2&q=a'))
  })

  it('goes to the plain list when nothing was carried, and never to an address that is not the list', () => {
    mountBoth({ initialEntries: ['/things/9'] })
    expect(screen.getByText('back').getAttribute('href')).toBe('/things')
    cleanup()
    mountBoth({ initialEntries: [{ pathname: '/things/9', state: { from: 'https://evil.example/' } }] })
    expect(screen.getByText('back').getAttribute('href')).toBe('/things')
    cleanup()
    mountBoth({ initialEntries: [{ pathname: '/things/9', state: { from: '/other?x=1' } }] })
    expect(screen.getByText('back').getAttribute('href')).toBe('/things')
  })
})
