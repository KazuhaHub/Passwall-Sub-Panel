import { useCallback, useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type RefObject } from 'react'
import { useSpacedT } from '@/i18n/spacing'
import { finalIndexFromInsertion, insertionIndex, reorderCall, type ReorderCall } from './dataTableLogic'
import type { RowGeometry } from './useWindowedRows'
import { settleDeltas, slideShift, translateYOf } from './reorderMotion'
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
// The slide of the other rows and the settle into place on release.
export const REORDER_MS = 150
const REORDER_EASING = 'cubic-bezier(0.2, 0, 0, 1)'
const REDUCED = '(prefers-reduced-motion: reduce)'

const prefersReducedMotion = () => typeof window.matchMedia === 'function' && window.matchMedia(REDUCED).matches

/**
 * Row reordering by pointer and by keyboard, with no drag-and-drop library:
 * pointer events cover mouse, pen and touch in one code path, and the keyboard
 * model (Space picks up, arrows move, Space drops, Esc cancels, with spoken
 * progress) is what a DnD library would add for accessibility.
 *
 * Both paths end in the same report, (movedId, beforeId), built by reorderCall.
 * While dragging, `insertion` is the slot the row would drop into (an index in
 * the list that still includes the dragged row) so the table can draw one line.
 *
 * THE FEEL. Unless the user asks for reduced motion, the carried row is lifted and follows the
 * pointer (its transform is written straight to the element: a React render per pointermove
 * would re-render every row), the rows it passes slide one row-height out of the way (`shiftOf`,
 * a transform the table applies with a 150ms transition), and on release every row settles from
 * where it was drawn to where it now belongs (FLIP, below). A keyboard carry gets the same
 * gap and the same settle, the carried row stepping a slot at a time. With reduced motion none
 * of that moves and the drop line is the only cue, as it was before.
 */
export function useRowReorder({ enabled, ids, nameOf, onReorder, bodyRef, scrollRef, geometry, reveal }: Options) {
  const { t } = useSpacedT()
  // `h` is the carried row's height: the distance the others slide to open the gap.
  const [dragging, setDragging] = useState<{ id: string; insertion: number; from: number; h: number; motion: boolean } | null>(null)
  const [lifted, setLifted] = useState<{ id: string; from: number; to: number; h: number; motion: boolean } | null>(null)
  const [announcement, setAnnouncement] = useState('')
  const stop = useRef<(() => void) | null>(null)
  useEffect(() => () => stop.current?.(), [])

  const total = ids.length

  const rowEls = useCallback(() => [...(bodyRef.current?.querySelectorAll<HTMLElement>('[data-row]') ?? [])], [bodyRef])
  const rowEl = useCallback((id: string) => rowEls().find(r => r.dataset.id === id), [rowEls])
  const heightOf = useCallback((el: Element | null | undefined) => geometry?.rowH ?? el?.getBoundingClientRect().height ?? 0, [geometry])

  // FLIP. Right before a session ends, the top of every drawn row (as drawn: transforms
  // included) is remembered; the layout effect below runs once the rows have their final
  // place and animates each one from the remembered top to that place. It works whether the
  // page reorders its data at once, later, or not at all (a cancel): a row that ends where it
  // was drawn simply has nothing to travel.
  const settle = useRef<Map<string, number> | null>(null)
  const carried = useRef<string | null>(null)
  const rememberTops = useCallback(() => {
    const tops = new Map<string, number>()
    for (const r of rowEls()) tops.set(r.dataset.id ?? '', r.getBoundingClientRect().top)
    settle.current = tops
  }, [rowEls])
  useLayoutEffect(() => {
    // The pointer path wrote its own transform on the carried row; it is the table's to clear.
    if (carried.current !== null) {
      const el = rowEl(carried.current)
      if (el) el.style.transform = ''
      carried.current = null
    }
    const before = settle.current
    if (!before) return
    settle.current = null
    const rows = rowEls()
    const after = new Map<string, number>()
    for (const r of rows) after.set(r.dataset.id ?? '', r.getBoundingClientRect().top)
    const deltas = settleDeltas(before, after)
    for (const r of rows) {
      const d = deltas.get(r.dataset.id ?? '')
      if (d === undefined || typeof r.animate !== 'function') continue
      r.animate([{ transform: `translateY(${d}px)` }, { transform: 'none' }], { duration: REORDER_MS, easing: REORDER_EASING })
    }
  })

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
    const motion = !prefersReducedMotion()
    const startRow = (e.currentTarget as HTMLElement | null)?.closest?.('tr') ?? rowEl(id)
    const h = heightOf(startRow)
    // Where on the row the pointer took hold, so the row stays under the same spot of the pointer.
    const grab = point.y - (startRow?.getBoundingClientRect().top ?? point.y)
    let applied = 0
    const session = (ins: number) => ({ id, insertion: ins, from, h, motion })
    setDragging(session(insertion))
    // The carried row follows the pointer. Its natural top is its drawn top minus the offset
    // already applied, which stays right while the list scrolls under a held pointer.
    const place = () => {
      if (!motion) return
      const el = rowEl(id)
      if (!el) return
      const natural = el.getBoundingClientRect().top - applied
      applied = point.y - grab - natural
      el.style.transform = `translateY(${applied}px)`
    }

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
        setDragging(session(insertion))
        place()
        return
      }
      // Midpoints of the rows where they LIE, not where they are drawn: the others are slid and
      // the carried one follows the pointer, and measuring those would feed the slot back into
      // itself. The offset a row currently has is read from its computed transform (which is
      // also right mid-transition) and taken off.
      const rows = rowEls()
      const mids = rows.map(r => {
        const b = r.getBoundingClientRect()
        return b.top + b.height / 2 - translateYOf(getComputedStyle(r).transform)
      })
      insertion = insertionIndex(mids, point.y)
      setDragging(session(insertion))
      place()
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
      window.removeEventListener('scroll', update, true)
      stop.current = null
      if (motion) {
        // Handed to the layout effect, which clears the transform this path wrote on the row.
        carried.current = id
        rememberTops()
      }
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
    // A held pointer over a list that scrolls (the wheel) moves rows under it without a pointermove.
    window.addEventListener('scroll', update, true)
    raf = requestAnimationFrame(loop)
    stop.current = () => end(false)
  }, [enabled, ids, onReorder, scrollRef, geometry, rowEls, rowEl, rememberTops, heightOf])

  const drop = (id: string, to: number) => {
    const call = reorderCall(ids, id, to)
    if (lifted?.motion) rememberTops()
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
        setLifted({ id, from, to: from, h: heightOf((e.currentTarget as HTMLElement | null)?.closest?.('tr') ?? rowEl(id)), motion: !prefersReducedMotion() })
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
      if (lifted.motion) rememberTops()
      setLifted(null)
      setAnnouncement(t('ui.data.reorder_cancelled', { name: nameOf(id) }))
    }
  }

  const onBlur = (id: string) => {
    if (lifted?.id === id) {
      if (lifted.motion) rememberTops()
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

  // The session that is moving rows, and the slot the carried row is over (its final index).
  const active = dragging ?? lifted
  const slot = dragging ? finalIndexFromInsertion(dragging.from, dragging.insertion) : lifted ? lifted.to : 0
  const motion = active?.motion === true

  /**
   * The vertical offset (px) the table draws row `id` at `index` with, or undefined when it is
   * not moving anything. Rows that stay put get 0, not undefined: that keeps their transition on
   * so they glide back when the slot changes. The pointer-carried row is absent on purpose, its
   * transform is the pointer's and is written to the element directly.
   */
  const shiftOf = (id: string, index: number): number | undefined => {
    if (!active || !motion) return undefined
    if (id === active.id) return lifted && !dragging ? (lifted.to - lifted.from) * lifted.h : undefined
    return slideShift(index, active.from, slot, active.h)
  }

  return { insertion, movingId, motion, shiftOf, announcement, onPointerDown, onKeyDown, onBlur }
}
