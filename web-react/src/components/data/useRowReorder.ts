import { useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { finalIndexFromInsertion, insertionIndex, reorderCall, type ReorderCall } from './dataTableLogic'

interface Options {
  enabled: boolean
  /** Ids of the rows in the order they are shown. */
  ids: readonly string[]
  nameOf: (id: string) => string
  onReorder: ((call: ReorderCall) => void) | undefined
  bodyRef: RefObject<HTMLTableSectionElement | null>
  scrollRef: RefObject<HTMLElement | null>
}

// Autoscroll when the pointer is within this many px of the scrolling frame's edge.
const EDGE = 36

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
export function useRowReorder({ enabled, ids, nameOf, onReorder, bodyRef, scrollRef }: Options) {
  const { t } = useTranslation('common')
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
    setDragging({ id, insertion })

    const move = (ev: PointerEvent) => {
      const rows = bodyRef.current?.querySelectorAll<HTMLElement>('[data-row]') ?? []
      const mids = [...rows].map(r => { const b = r.getBoundingClientRect(); return b.top + b.height / 2 })
      insertion = insertionIndex(mids, ev.clientY)
      setDragging({ id, insertion })
      const frame = scrollRef.current
      if (frame) {
        const b = frame.getBoundingClientRect()
        if (b.height > 0) {
          if (ev.clientY < b.top + EDGE) frame.scrollTop -= 14
          else if (ev.clientY > b.bottom - EDGE) frame.scrollTop += 14
        }
      }
    }
    const end = (commit: boolean) => {
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
    const up = () => end(true)
    const cancel = () => end(false)
    const key = (ev: KeyboardEvent) => { if (ev.key === 'Escape') end(false) }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', cancel)
    window.addEventListener('keydown', key)
    stop.current = () => end(false)
  }, [enabled, ids, onReorder, bodyRef, scrollRef])

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
    if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
      e.preventDefault()
      const to = Math.min(Math.max(0, lifted.to + (e.key === 'ArrowDown' ? 1 : -1)), total - 1)
      setLifted({ ...lifted, to })
      setAnnouncement(t('ui.data.reorder_moved', { name: nameOf(id), pos: to + 1, total }))
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
