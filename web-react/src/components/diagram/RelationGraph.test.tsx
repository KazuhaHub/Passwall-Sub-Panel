// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RelationGraph, edgePath, layoutGraph, type GraphColumn, type GraphEdge } from './RelationGraph'

afterEach(cleanup)

const columns: GraphColumn[] = [
  { id: 'entries', title: 'Entries', boxes: [{ id: 'e1', label: 'Entry 1' }, { id: 'e2', label: 'Entry 2' }] },
  { id: 'landings', title: 'Landings', boxes: [{ id: 'l1', label: 'Landing 1' }, { id: 'l2', label: 'Landing 2' }, { id: 'l3', label: 'Landing 3' }] },
]
const edges: GraphEdge[] = [
  { from: 'e1', to: 'l1', style: 'solid', status: 'success' },
  { from: 'e1', to: 'l2', style: 'dashed' },
  { from: 'e2', to: 'l3', style: 'solid', status: 'danger' },
]

describe('layoutGraph', () => {
  it('stacks boxes in columns left to right and reports the size to scroll', () => {
    const g = layoutGraph(columns)
    expect(g.boxes.e1.x).toBeLessThan(g.boxes.l1.x)
    expect(g.boxes.l2.y).toBeGreaterThan(g.boxes.l1.y)
    expect(g.boxes.e1.y).toBe(g.boxes.l1.y)
    // height follows the longest column, so 30 boxes make it taller, not smaller
    const tall = layoutGraph([{ id: 'a', title: 'A', boxes: Array.from({ length: 30 }, (_, i) => ({ id: `a${i}`, label: String(i) })) }])
    expect(tall.height).toBeGreaterThan(g.height * 4)
    expect(tall.boxes.a0.h).toBe(g.boxes.e1.h)
  })
})

describe('edgePath', () => {
  it('leaves the right side of the left box and enters the left side of the right box', () => {
    const g = layoutGraph(columns)
    const d = edgePath(g.boxes.e1, g.boxes.l2)
    expect(d.startsWith(`M ${g.boxes.e1.x + g.boxes.e1.w} `)).toBe(true)
    expect(d.endsWith(` ${g.boxes.l2.x} ${g.boxes.l2.y + g.boxes.l2.h / 2}`)).toBe(true)
  })
})

describe('RelationGraph', () => {
  it('draws one connector per edge with its style and status', () => {
    const { container } = render(<RelationGraph columns={columns} edges={edges} />)
    const paths = container.querySelectorAll('path[data-edge]')
    expect(paths).toHaveLength(3)
    expect(paths[1].getAttribute('data-style')).toBe('dashed')
    expect(paths[2].getAttribute('data-tone')).toBe('danger')
  })

  it('selecting a box keeps its edges and dims the rest', () => {
    const { container } = render(<RelationGraph columns={columns} edges={edges} />)
    fireEvent.click(screen.getByRole('button', { name: /Entry 1/ }))
    const paths = [...container.querySelectorAll('path[data-edge]')]
    expect(paths.map(p => p.getAttribute('data-dim'))).toEqual([null, null, 'true'])
    // connected boxes stay, strangers dim
    expect(screen.getByRole('button', { name: /Landing 1/ }).getAttribute('data-dim')).toBeNull()
    expect(screen.getByRole('button', { name: /Landing 3/ }).getAttribute('data-dim')).toBe('true')
    expect(screen.getByRole('button', { name: /Entry 1/ }).getAttribute('aria-pressed')).toBe('true')
  })

  it('selecting again, or pressing Escape, clears the selection', () => {
    const { container } = render(<RelationGraph columns={columns} edges={edges} />)
    const box = screen.getByRole('button', { name: /Entry 1/ })
    fireEvent.click(box)
    fireEvent.click(box)
    expect(container.querySelectorAll('[data-dim]')).toHaveLength(0)
    fireEvent.click(box)
    fireEvent.keyDown(box, { key: 'Escape' })
    expect(container.querySelectorAll('[data-dim]')).toHaveLength(0)
  })

  it('works controlled too', () => {
    const onSelect = vi.fn()
    const { container, rerender } = render(<RelationGraph columns={columns} edges={edges} selectedId="l3" onSelect={onSelect} />)
    expect([...container.querySelectorAll('path[data-edge]')].map(p => p.getAttribute('data-dim'))).toEqual(['true', 'true', null])
    fireEvent.click(screen.getByRole('button', { name: /Entry 2/ }))
    expect(onSelect).toHaveBeenCalledWith('e2')
    rerender(<RelationGraph columns={columns} edges={edges} selectedId={null} onSelect={onSelect} />)
    expect(container.querySelectorAll('[data-dim]')).toHaveLength(0)
  })

  it('has one tab stop and moves with the arrow keys', () => {
    render(<RelationGraph columns={columns} edges={edges} />)
    const boxes = screen.getAllByRole('button')
    expect(boxes.filter(b => b.getAttribute('tabindex') === '0')).toHaveLength(1)
    const e1 = screen.getByRole('button', { name: /Entry 1/ })
    e1.focus()
    fireEvent.keyDown(e1, { key: 'ArrowDown' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: /Entry 2/ }))
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: /Landing 2/ }))
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    expect(document.activeElement).toBe(screen.getByRole('button', { name: /Entry 2/ }))
  })

  it('announces what a selection is connected to', () => {
    render(<RelationGraph columns={columns} edges={edges} />)
    fireEvent.click(screen.getByRole('button', { name: /Entry 1/ }))
    expect(screen.getByRole('status').textContent).toContain('Entry 1 selected, 2 connections')
  })

  it('stays usable with 30 boxes per column: all rendered, scrolling container', () => {
    const big: GraphColumn[] = ['a', 'b', 'c'].map(c => ({
      id: c, title: c, boxes: Array.from({ length: 30 }, (_, i) => ({ id: `${c}${i}`, label: `${c} box ${i}` })),
    }))
    const bigEdges: GraphEdge[] = Array.from({ length: 30 }, (_, i) => ({ from: `a${i}`, to: `b${(i * 7) % 30}` }))
    const { container } = render(<RelationGraph columns={big} edges={bigEdges} />)
    expect(screen.getAllByRole('button')).toHaveLength(90)
    expect(container.querySelectorAll('path[data-edge]')).toHaveLength(30)
    expect((container.firstElementChild as HTMLElement).className).toContain('overflow-auto')
  })
})
