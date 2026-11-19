// @vitest-environment jsdom
import '@/test/initI18n'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RelationGraph, edgePath, layoutGraph, pathsThrough, type GraphColumn, type GraphEdge } from './RelationGraph'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const columns: GraphColumn[] = [
  { id: 'entries', title: 'Entries', boxes: [{ id: 'e1', label: 'Entry 1' }, { id: 'e2', label: 'Entry 2' }] },
  { id: 'landings', title: 'Landings', boxes: [{ id: 'l1', label: 'Landing 1' }, { id: 'l2', label: 'Landing 2' }, { id: 'l3', label: 'Landing 3' }] },
]
const edges: GraphEdge[] = [
  { id: 'a', from: 'e1', to: 'l1', style: 'solid', status: 'success' },
  { id: 'b', from: 'e1', to: 'l2', style: 'dashed' },
  { id: 'c', from: 'e2', to: 'l3', style: 'solid', status: 'danger' },
]
const listRegion = () => screen.getByRole('region', { name: 'Relationship graph lines' })
const box = (name: string) => screen.getByRole('button', { name: new RegExp(`^${name},`) })

describe('layoutGraph', () => {
  it('stacks boxes in columns left to right and reports the size to scroll', () => {
    const g = layoutGraph(columns)
    expect(g.boxes.e1.x).toBeLessThan(g.boxes.l1.x)
    expect(g.boxes.l2.y).toBeGreaterThan(g.boxes.l1.y)
    expect(g.boxes.e1.y).toBe(g.boxes.l1.y)
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

  it('shifts a parallel line sideways so two lines between the same boxes are two curves', () => {
    const g = layoutGraph(columns)
    expect(edgePath(g.boxes.e1, g.boxes.l1, -8)).not.toBe(edgePath(g.boxes.e1, g.boxes.l1, 8))
    expect(edgePath(g.boxes.e1, g.boxes.l1, 0)).toBe(edgePath(g.boxes.e1, g.boxes.l1))
  })
})

describe('RelationGraph', () => {
  it('draws one connector per edge with its style, status and id', () => {
    const { container } = render(<RelationGraph columns={columns} edges={edges} />)
    const paths = container.querySelectorAll('path[data-edge]')
    expect(paths).toHaveLength(3)
    expect(paths[1].getAttribute('data-style')).toBe('dashed')
    expect(paths[2].getAttribute('data-tone')).toBe('danger')
    expect([...paths].map(p => p.getAttribute('data-edge-id'))).toEqual(['a', 'b', 'c'])
  })

  it('selecting a box keeps its edges and dims the rest', () => {
    const { container } = render(<RelationGraph columns={columns} edges={edges} layout="canvas" />)
    fireEvent.click(box('Entry 1'))
    expect([...container.querySelectorAll('path[data-edge]')].map(p => p.getAttribute('data-dim'))).toEqual([null, null, 'true'])
    expect(box('Landing 1').getAttribute('data-dim')).toBeNull()
    expect(box('Landing 3').getAttribute('data-dim')).toBe('true')
    expect(box('Entry 1').getAttribute('aria-pressed')).toBe('true')
  })

  it('selecting again, or pressing Escape, clears the selection', () => {
    const { container } = render(<RelationGraph columns={columns} edges={edges} />)
    fireEvent.click(box('Entry 1'))
    fireEvent.click(box('Entry 1'))
    expect(container.querySelectorAll('[data-dim]')).toHaveLength(0)
    fireEvent.click(box('Entry 1'))
    fireEvent.keyDown(box('Entry 1'), { key: 'Escape' })
    expect(container.querySelectorAll('[data-dim]')).toHaveLength(0)
  })

  it('works controlled too', () => {
    const onSelect = vi.fn()
    const { container, rerender } = render(<RelationGraph columns={columns} edges={edges} selectedId="l3" onSelect={onSelect} layout="canvas" />)
    expect([...container.querySelectorAll('path[data-edge]')].map(p => p.getAttribute('data-dim'))).toEqual(['true', 'true', null])
    fireEvent.click(box('Entry 2'))
    expect(onSelect).toHaveBeenCalledWith('e2')
    rerender(<RelationGraph columns={columns} edges={edges} selectedId={null} onSelect={onSelect} layout="canvas" />)
    expect(container.querySelectorAll('[data-dim]')).toHaveLength(0)
  })

  it('has one tab stop and moves with the arrow keys', () => {
    render(<RelationGraph columns={columns} edges={edges} layout="canvas" />)
    expect(screen.getAllByRole('button').filter(b => b.getAttribute('tabindex') === '0')).toHaveLength(1)
    box('Entry 1').focus()
    fireEvent.keyDown(box('Entry 1'), { key: 'ArrowDown' })
    expect(document.activeElement).toBe(box('Entry 2'))
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(box('Landing 2'))
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    expect(document.activeElement).toBe(box('Entry 2'))
  })

  it('announces how many lines pass through the selection', () => {
    render(<RelationGraph columns={columns} edges={edges} />)
    fireEvent.click(box('Entry 1'))
    expect(screen.getByRole('status').textContent).toContain('Entry 1 selected, 2 lines pass through')
  })

  it('stays usable with 30 boxes per column: all rendered, scrolling container', () => {
    const big: GraphColumn[] = ['a', 'b', 'c'].map(c => ({
      id: c, title: c, boxes: Array.from({ length: 30 }, (_, i) => ({ id: `${c}${i}`, label: `${c} box ${i}` })),
    }))
    const bigEdges: GraphEdge[] = Array.from({ length: 30 }, (_, i) => ({ id: `g${i}`, from: `a${i}`, to: `b${(i * 7) % 30}` }))
    const { container } = render(<RelationGraph columns={big} edges={bigEdges} layout="canvas" />)
    expect(screen.getAllByRole('button')).toHaveLength(90)
    expect(container.querySelectorAll('path[data-edge]')).toHaveLength(30)
    expect(container.querySelector('.overflow-auto')).not.toBeNull()
  })

  describe('the lines list (a text equivalent that does not run out)', () => {
    it('is always there, and asks for a selection until there is one', () => {
      render(<RelationGraph columns={columns} edges={edges} />)
      expect(listRegion().textContent).toContain('Select a box to list the lines through it here')
      expect(listRegion().tabIndex).toBe(0)
    })

    it('lists every line through the selected box, none cut off, each with its own kind and problem', () => {
      const big: GraphColumn[] = [
        { id: 'a', title: 'A', boxes: [{ id: 'hub', label: 'Hub' }] },
        { id: 'b', title: 'B', boxes: Array.from({ length: 12 }, (_, i) => ({ id: `t${i}`, label: `Target ${i}` })) },
      ]
      const bigEdges: GraphEdge[] = Array.from({ length: 12 }, (_, i) => ({ id: `h${i}`, from: 'hub', to: `t${i}`, style: i === 0 ? 'dashed' : 'solid', status: i === 1 ? 'danger' : undefined }))
      render(<RelationGraph columns={big} edges={bigEdges} selectedId="hub" />)
      const items = within(listRegion()).getAllByRole('listitem')
      expect(items).toHaveLength(12)
      expect(items[0].textContent).toContain('encrypted tunnel')
      expect(items[1].textContent).toContain('interrupted')
      expect(items[11].textContent).toContain('Target 11')
    })

    it('lets you move to any box on a path', () => {
      const onSelect = vi.fn()
      render(<RelationGraph columns={columns} edges={edges} selectedId="e1" onSelect={onSelect} />)
      fireEvent.click(within(listRegion()).getAllByRole('button', { name: 'Landing 2' })[0])
      expect(onSelect).toHaveBeenCalledWith('l2')
    })

    it('says so when no line passes through the selected box', () => {
      render(<RelationGraph columns={columns} edges={[]} selectedId="e2" />)
      expect(listRegion().textContent).toContain('No lines pass through Entry 2')
    })
  })

  describe('phone layout', () => {
    const stub = (matches: boolean) => {
      window.matchMedia = vi.fn().mockImplementation((q: string) => ({
        matches, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent() { return false }, onchange: null,
      })) as unknown as typeof window.matchMedia
    }
    afterEach(() => { delete (window as { matchMedia?: unknown }).matchMedia })

    it('is a list of columns, not a canvas that scrolls two columns off screen', () => {
      stub(true)
      const { container } = render(<RelationGraph columns={columns} edges={edges} />)
      expect(container.querySelector('svg')).toBeNull()
      expect(screen.getByRole('region', { name: 'Entries' })).toBeTruthy()
      expect(screen.getByRole('region', { name: 'Landings' })).toBeTruthy()
      fireEvent.click(box('Entry 1'))
      expect(box('Entry 1').getAttribute('aria-pressed')).toBe('true')
      expect(box('Landing 3').getAttribute('data-dim')).toBe('true')
      expect(within(listRegion()).getAllByRole('listitem')).toHaveLength(2)
    })

    it('keeps the canvas on a wide screen', () => {
      stub(false)
      const { container } = render(<RelationGraph columns={columns} edges={edges} />)
      expect(container.querySelector('svg')).not.toBeNull()
    })

    it('can be forced either way', () => {
      stub(false)
      const { container } = render(<RelationGraph columns={columns} edges={edges} layout="list" />)
      expect(container.querySelector('svg')).toBeNull()
    })
  })
})

describe('full paths through a box', () => {
  const cols: GraphColumn[] = [
    { id: 'e', title: 'Entries', boxes: [{ id: 'e1', label: 'Entry 1', status: 'danger' }, { id: 'e2', label: 'Entry 2' }, { id: 'e3', label: 'Entry 3' }] },
    { id: 'l', title: 'Landings', boxes: [{ id: 'l1', label: 'Landing 1' }, { id: 'l2', label: 'Landing 2' }] },
    { id: 'x', title: 'Exits', boxes: [{ id: 'x1', label: 'Exit 1' }, { id: 'x2', label: 'Exit 2' }] },
  ]
  const es: GraphEdge[] = [
    { id: 'p1', from: 'e1', to: 'l1' }, { id: 'p2', from: 'e2', to: 'l1' }, { id: 'p3', from: 'e3', to: 'l2' },
    { id: 'p4', from: 'l1', to: 'x1' }, { id: 'p5', from: 'l2', to: 'x2' },
  ]
  const nodesOf = (paths: ReturnType<typeof pathsThrough>) => paths.map(p => p.nodes.join('>')).sort()

  it('finds paths in both directions', () => {
    expect(nodesOf(pathsThrough('e1', es))).toEqual(['e1>l1>x1'])
    expect(nodesOf(pathsThrough('x1', es))).toEqual(['e1>l1>x1', 'e2>l1>x1'])
    expect(nodesOf(pathsThrough('l1', es))).toEqual(['e1>l1>x1', 'e2>l1>x1'])
    expect(pathsThrough('nobody', es)).toEqual([])
  })

  it('selecting an entry lights its landing AND that landing\'s exit, and dims the rest', () => {
    const { container } = render(<RelationGraph columns={cols} edges={es} selectedId="e1" layout="canvas" />)
    expect(box('Landing 1').getAttribute('data-dim')).toBeNull()
    expect(box('Exit 1').getAttribute('data-dim')).toBeNull()
    expect(box('Landing 2').getAttribute('data-dim')).toBe('true')
    expect(box('Exit 2').getAttribute('data-dim')).toBe('true')
    expect([...container.querySelectorAll('path[data-edge]')].map(p => p.getAttribute('data-dim'))).toEqual([null, 'true', 'true', null, 'true'])
  })

  it('selecting an exit reaches back to every entry that ends there', () => {
    render(<RelationGraph columns={cols} edges={es} selectedId="x1" layout="canvas" />)
    expect(box('Entry 1').getAttribute('data-dim')).toBeNull()
    expect(box('Entry 2').getAttribute('data-dim')).toBeNull()
    expect(box('Entry 3').getAttribute('data-dim')).toBe('true')
  })

  it('lists every path in the lines list, node by node', () => {
    render(<RelationGraph columns={cols} edges={es} selectedId="x1" layout="canvas" />)
    const items = within(listRegion()).getAllByRole('listitem')
    expect(items.map(i => i.textContent?.replace(/\s*→\s*/g, ' → ').replace(/\s+/g, ' ').trim())).toEqual(['Entry 1 → Landing 1 → Exit 1', 'Entry 2 → Landing 1 → Exit 1'])
  })

  it('counts lines the same way everywhere (the paths through a box): in its name and in the announcement', () => {
    render(<RelationGraph columns={cols} edges={es} layout="canvas" />)
    expect(box('Entry 1').getAttribute('aria-label')).toBe('Entry 1, interrupted, 1 line passes through')
    expect(box('Landing 1').getAttribute('aria-label')).toBe('Landing 1, 2 lines pass through')
    fireEvent.click(box('Landing 1'))
    expect(screen.getByRole('status').textContent).toContain('Landing 1 selected, 2 lines pass through')
  })

  it('softens unrelated boxes with colour, not opacity', () => {
    render(<RelationGraph columns={cols} edges={es} selectedId="e1" layout="canvas" />)
    expect(box('Landing 2').className).toContain('text-foreground/70')
    expect(box('Landing 2').className).not.toMatch(/opacity-/)
  })
})

describe('parallel lines: the same entry and landing joined by more than one line', () => {
  const cols: GraphColumn[] = [
    { id: 'e', title: 'Entries', boxes: [{ id: 'e1', label: 'Entry 1' }] },
    { id: 'l', title: 'Landings', boxes: [{ id: 'l1', label: 'Landing 1' }] },
    { id: 'x', title: 'Exits', boxes: [{ id: 'x1', label: 'Exit 1' }] },
  ]
  const two: GraphEdge[] = [
    { id: 'fwd', from: 'e1', to: 'l1', style: 'solid', label: 'Forward' },
    { id: 'tun', from: 'e1', to: 'l1', style: 'dashed', status: 'danger', label: 'Tunnel' },
    { id: 'out', from: 'l1', to: 'x1' },
  ]
  const three: GraphEdge[] = [...two, { id: 'cdn', from: 'e1', to: 'l1', style: 'solid', status: 'pending', label: 'CDN line' }]

  it('keeps each line as its own path, with its own id', () => {
    expect(pathsThrough('e1', two).map(p => p.edges.map(e => e.id).join('+')).sort()).toEqual(['fwd+out', 'tun+out'])
    expect(pathsThrough('l1', three)).toHaveLength(3)
  })

  it('lists each line once, with its OWN kind and problem, and no duplicate keys', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})
    render(<RelationGraph columns={cols} edges={two} selectedId="e1" />)
    const items = within(listRegion()).getAllByRole('listitem')
    expect(items).toHaveLength(2)
    const fwd = items.find(i => i.textContent?.includes('Forward'))!
    const tun = items.find(i => i.textContent?.includes('Tunnel'))!
    expect(tun.textContent).toContain('encrypted tunnel')
    expect(tun.textContent).toContain('interrupted')
    expect(fwd.textContent).not.toContain('encrypted tunnel')
    expect(fwd.textContent).not.toContain('interrupted')
    expect(err).not.toHaveBeenCalled()
  })

  it('draws parallel lines as separate curves, and lights them all with the box', () => {
    const { container } = render(<RelationGraph columns={cols} edges={three} selectedId="e1" layout="canvas" />)
    const drawn = [...container.querySelectorAll('path[data-edge]')]
    expect(drawn).toHaveLength(4)
    const parallel = drawn.filter(p => ['fwd', 'tun', 'cdn'].includes(p.getAttribute('data-edge-id')!)).map(p => p.getAttribute('d'))
    expect(new Set(parallel).size).toBe(3)
    expect(drawn.every(p => p.getAttribute('data-dim') === null)).toBe(true)
  })

  it('counts lines, not boxes', () => {
    render(<RelationGraph columns={cols} edges={three} layout="canvas" />)
    expect(box('Entry 1').getAttribute('aria-label')).toBe('Entry 1, 3 lines pass through')
  })
})
