// @vitest-environment jsdom
import '@/test/initI18n'
import { act, cleanup, fireEvent, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderInRouter } from '@/test/routerHarness'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { DetailSheet } from './DetailSheet'

afterEach(cleanup)

function Page() {
  const sel = useUrlSelection('node')
  return (
    <div>
      <button onClick={() => sel.select('7')}>open row</button>
      <DetailSheet
        open={sel.isOpen}
        onClose={sel.clear}
        title={`Node ${sel.selected}`}
        status={<span>Online</span>}
        actions={<button>Edit</button>}
        footer={<button>Done</button>}
      >
        <p>Body text</p>
      </DetailSheet>
    </div>
  )
}

describe('DetailSheet', () => {
  it('renders nothing while closed', () => {
    renderInRouter(<Page />, ['/nodes'])
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('opens from the URL, titled, with status, actions, body and footer', () => {
    renderInRouter(<Page />, ['/nodes?node=7'])
    const dialog = screen.getByRole('dialog', { name: 'Node 7' })
    expect(dialog.textContent).toContain('Online')
    expect(dialog.textContent).toContain('Body text')
    expect(screen.getByRole('button', { name: 'Edit' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Done' })).toBeTruthy()
  })

  it('has a labelled close button', () => {
    renderInRouter(<Page />, ['/nodes?node=7'])
    expect(screen.getByRole('button', { name: 'Close details' })).toBeTruthy()
  })

  it('closes with its button: through Back when the row opened it', async () => {
    const { router } = renderInRouter(<Page />, ['/nodes'])
    fireEvent.click(screen.getByRole('button', { name: 'open row' }))
    await act(async () => { await Promise.resolve() })
    expect(router.state.location.search).toBe('?node=7')
    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    await act(async () => { await Promise.resolve() })
    expect(router.state.location.search).toBe('')
  })

  it('closes on Escape', async () => {
    const onClose = vi.fn()
    renderInRouter(<DetailSheet open onClose={onClose} title="T"><p>x</p></DetailSheet>)
    await act(async () => { fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' }) })
    expect(onClose).toHaveBeenCalled()
  })

  it('closes when the browser goes Back', async () => {
    const { router } = renderInRouter(<Page />, ['/nodes'])
    fireEvent.click(screen.getByRole('button', { name: 'open row' }))
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole('dialog')).toBeTruthy()
    await act(async () => { await router.navigate(-1) })
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
