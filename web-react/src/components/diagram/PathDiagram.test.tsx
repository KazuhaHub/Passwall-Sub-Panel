// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { PathDiagram } from './PathDiagram'
import type { PathEdge, PathNode } from './pathSummary'

afterEach(cleanup)

const nodes: PathNode[] = [
  { id: 'u', kind: 'user', label: 'User' },
  { id: 'e', kind: 'entry', label: 'Taiwan HiNet Dynamic', sublabel: ':10443', status: 'success' },
  { id: 'l', kind: 'landing', label: 'Irvine Home', landingColor: { index: 7, cycle: 0 } },
]
const edges: PathEdge[] = [{ style: 'solid' }, { style: 'dashed', label: '48 ms', status: 'warning' }]

describe('PathDiagram', () => {
  it('is one image whose accessible name is the generated sentence', () => {
    render(<PathDiagram nodes={nodes} edges={edges} />)
    const img = screen.getByRole('img')
    expect(img.getAttribute('aria-label')).toBe('User through Taiwan HiNet Dynamic (:10443) by encrypted tunnel (48 ms, needs attention) to Irvine Home')
  })

  it('lets the caller supply the sentence', () => {
    render(<PathDiagram nodes={nodes} edges={edges} summary="Custom summary" />)
    expect(screen.getByRole('img', { name: 'Custom summary' })).toBeTruthy()
  })

  it('shows every node label and sublabel as text, so the picture is never the only source', () => {
    const { container } = render(<PathDiagram nodes={nodes} edges={edges} />)
    expect(container.textContent).toContain('Taiwan HiNet Dynamic')
    expect(container.textContent).toContain(':10443')
    expect(container.textContent).toContain('48 ms')
  })

  it('draws solid and dashed edges differently and exposes the style', () => {
    const { container } = render(<PathDiagram nodes={nodes} edges={edges} />)
    const drawn = container.querySelectorAll('[data-edge]')
    expect(drawn).toHaveLength(2)
    expect(drawn[0].getAttribute('data-edge')).toBe('solid')
    expect(drawn[1].getAttribute('data-edge')).toBe('dashed')
    expect(drawn[1].getAttribute('data-tone')).toBe('warning')
    // every edge carries a text alternative of its own
    expect(drawn[1].getAttribute('title')).toContain('encrypted tunnel')
  })

  it('marks size and orientation for the layout and for tests', () => {
    const { container, rerender } = render(<PathDiagram nodes={nodes} edges={edges} size="sm" />)
    const root = container.firstElementChild as HTMLElement
    expect(root.getAttribute('data-size')).toBe('sm')
    expect(root.getAttribute('data-orientation')).toBe('horizontal')
    rerender(<PathDiagram nodes={nodes} edges={edges} size="md" orientation="vertical" />)
    expect((container.firstElementChild as HTMLElement).getAttribute('data-orientation')).toBe('vertical')
  })

  it('keeps the end nodes icon-only in the small size, with the label still available', () => {
    const { container } = render(<PathDiagram nodes={[...nodes, { id: 'i', kind: 'internet', label: 'Internet' }]} edges={[...edges, { style: 'solid' }]} size="sm" />)
    const first = container.querySelector('[data-node="u"]') as HTMLElement
    expect(first.getAttribute('title')).toBe('User')
    expect(within(first).queryByText('User')).toBeNull()
    expect(container.querySelector('[data-node="e"]')!.textContent).toContain('Taiwan HiNet Dynamic')
  })

  it('tolerates missing edges by drawing plain solid ones', () => {
    const { container } = render(<PathDiagram nodes={nodes} />)
    expect(container.querySelectorAll('[data-edge="solid"]')).toHaveLength(2)
  })
})
