import { useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type RefObject } from 'react'
import { useSpacedT } from '@/i18n/spacing'
import { finalIndexFromInsertion, insertionIndex, reorderCall, type ReorderCall } from './dataTableLogic'
import type { RowGeometry } from './useWindowedRows'
import { autoScrollStep, carryTarget, insertionAtOffset } from './windowing'

interface Options {
  enabled: boolean
  /** Ids of the rows in the order they are shown. */
  ids: readonly string[]
  nameOf: (id: string) => string
  onReorder: ((call: ReorderCall) => void) | undefined
  bodyRef: RefObject<HTMLTableSectionElement | null>
  scrollRef: RefObject<HTMLElement | null>
  /**
   * Given when the table is windowed. Rows are then not all in the DOM, so the slot
   * under the pointer is computed from the fixed row height, the scroll offset and the
   * pointer, never from row elements, and the scroll frame comes from here too (it may
   * be the page itself). Without it the rows' own boxes are measured, as before.
   */
  geometry?: RowGeometry
  /** Scroll the row at this index into view (a carried row's target can be far off screen). */
  reveal?: (index: number) => void
}

// Autoscroll when the pointer is within this many px of the scrolling frame's edge;
// a release further than OUTSIDE px beyond the frame cancels the drag.
const EDGE = 36
const OUTSIDE = 64
// Windowed lists are long, so auto-scroll ramps up the longer the pointer is held (see autoScrollStep).
const GEOMETRY_STEP = 24
// Rows per PageUp / PageDown while carrying when the table cannot say how many fit.
const DEFAULT_PAGE = 10

/**
 * Row reordering by pointer and by keyboard, with no drag-and-drop library:
 * pointer events cover mouse, pen and touch in one code path, and the keyboard
 * model (Space picks up, arrows move, Space drops, Esc cancels, with spoken
 * progress) is what a DnD library would add for accessibility.
 *
 * Both paths end in the same report, (movedId, beforeId), built by reorderCall.
 * While dragging, `insertion` is the slot the row would drop into (an index in
 * the list that still includes the dragged row) so the table can draw one line.
 */
export function useRowReorder({ enabled, ids, nameOf, onReorder, bodyRef, scrollRef, geometry, reveal }: Options) {
  const { t } = useSpacedT()
  const [dragging, setDragging] = useState<{ id: string; insertion: number } | null>(null)
  const [lifted, setLifted] = useState<{ id: string; from: number; to: number } | null>(null)
  const [announcement, setAnnouncement] = useState('')
  const stop = useRef<(() => void) | null>(null)
  useEffect(() => () => stop.current?.(), [])

  const total = ids.length

  const onPointerDown = useCallback((e: ReactPointerEvent, id: string) => {
    if (!enabled || e.button !== 0) return
    // Stops the browser starting a text selection or a scroll gesture from the handle.
    e.preventDefault()
    const from = ids.indexOf(id)
    if (from < 0) return
    let insertion = from
    let point = { x: e.clientX, y: e.clientY }
    let raf = 0
    // When the pointer first entered an edge zone, for the ramp; 0 while it is clear of both.
    let heldSince = 0
    setDragging({ id, insertion })

    const near = () => {
      const frame = scrollRef.current
      if (!frame) return 0
      const b = frame.getBoundingClientRect()
      if (b.height <= 0) return 0
      if (point.y < b.top + EDGE) return -1
      if (point.y > b.bottom - EDGE) return 1
      return 0
    }
    const update = () => {
      if (geometry) {
        insertion = insertionAtOffset(point.y - geometry.bodyTop(), geometry.rowH, geometry.count)
        setDragging({ id, insertion })
        return
      }
      const rows = bodyRef.current?.querySelectorAll<HTMLElement>('[data-row]') ?? []
      const mids = [...rows].map(r => { const b = r.getBoundingClientRect(); return b.top + b.height / 2 })
      insertion = insertionIndex(mids, point.y)
      setDragging({ id, insertion })
    }
    // Scrolling must go on while the pointer is held still at an edge, and a
    // pointermove only arrives when it moves: so a frame loop does the scrolling.
    const loop = () => {
      if (geometry) {
        const f = geometry.frame()
        const now = performance.now()
        const step = autoScrollStep({ y: point.y, top: f.top, bottom: f.bottom, edge: EDGE, maxStep: GEOMETRY_STEP, heldMs: heldSince ? now - heldSince : 0 })
        if (step !== 0) {
          if (!heldSince) heldSince = now
          geometry.scrollBy(step)
          update()
        } else heldSince = 0
        raf = requestAnimationFrame(loop)
        return
      }
      const dir = near()
      if (dir !== 0 && scrollRef.current) {
        scrollRef.current.scrollTop += dir * 14
        update()
      }
      raf = requestAnimationFrame(loop)
    }
    const move = (ev: PointerEvent) => { point = { x: ev.clientX, y: ev.clientY }; update() }
    const outside = (ev: PointerEvent) => {
      const b = geometry ? geometry.frame() : scrollRef.current?.getBoundingClientRect()
      if (!b || (b.width === 0 && b.height === 0)) return false
      return ev.clientX < b.left - OUTSIDE || ev.clientX > b.right + OUTSIDE || ev.clientY < b.top - OUTSIDE || ev.clientY > b.bottom + OUTSIDE
    }
    const end = (commit: boolean) => {
      cancelAnimationFrame(raf)
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', cancel)
      window.removeEventListener('keydown', key)
      stop.current = null
      setDragging(null)
      if (commit) {
        const call = reorderCall(ids, id, finalIndexFromInsertion(from, insertion))
        if (call) onReorder?.(call)
      }
    }
    // Letting go far from the table is how a drag is abandoned.
    const up = (ev: PointerEvent) => end(!outside(ev))
    const cancel = () => end(false)
    const key = (ev: KeyboardEvent) => { if (ev.key === 'Escape') end(false) }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', cancel)
    window.addEventListener('keydown', key)
    raf = requestAnimationFrame(loop)
    stop.current = () => end(false)
  }, [enabled, ids, onReorder, bodyRef, scrollRef, geometry])

  const drop = (id: string, to: number) => {
    const call = reorderCall(ids, id, to)
    setLifted(null)
    setAnnouncement(t('ui.data.reorder_dropped', { name: nameOf(id), pos: to + 1 }))
    if (call) onReorder?.(call)
  }

  const onKeyDown = (e: ReactKeyboardEvent, id: string) => {
    if (!enabled) return
    const from = ids.indexOf(id)
    if (!lifted || lifted.id !== id) {
      if (e.key === ' ' || e.key === 'Enter') {
        e.preventDefault()
        setLifted({ id, from, to: from })
        setAnnouncement(t('ui.data.reorder_picked', { name: nameOf(id), pos: from + 1, total }))
      }
      return
    }
    // Arrows move one slot; Home / End the first / last; PageUp / PageDown one viewport of rows.
    const to = carryTarget(e.key, lifted.to, total, geometry?.page() ?? DEFAULT_PAGE)
    if (to !== null) {
      e.preventDefault()
      setLifted({ ...lifted, to })
      setAnnouncement(t('ui.data.reorder_moved', { name: nameOf(id), pos: to + 1, total }))
      reveal?.(to)
    } else if (e.key === ' ' || e.key === 'Enter') {
      e.preventDefault()
      drop(id, lifted.to)
    } else if (e.key === 'Escape') {
      // Esc must not also close a sheet or dialog the table sits in.
      e.preventDefault()
      e.stopPropagation()
      setLifted(null)
      setAnnouncement(t('ui.data.reorder_cancelled', { name: nameOf(id) }))
    }
  }

  const onBlur = (id: string) => {
    if (lifted?.id === id) {
      setLifted(null)
      setAnnouncement(t('ui.data.reorder_cancelled', { name: nameOf(id) }))
    }
  }

  // Where the drop line is drawn: the pointer's slot, or the keyboard's target slot
  // translated into the same "index in the list that still includes the row" terms.
  const insertion = dragging
    ? dragging.insertion
    : lifted
      ? (lifted.to > lifted.from ? lifted.to + 1 : lifted.to)
      : null
  const movingId = dragging?.id ?? lifted?.id ?? null

  return { insertion, movingId, announcement, onPointerDown, onKeyDown, onBlur }
}
