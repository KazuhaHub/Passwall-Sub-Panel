import { useCallback, useLayoutEffect, useMemo, useRef, useState, type RefObject } from 'react'
import { OVERSCAN, pageRows, revealScrollTop, rowIndexAtOffset, runPitch, windowRange } from './windowing'

export interface FrameRect { top: number; bottom: number; left: number; right: number; width: number; height: number }

/** What useRowReorder needs to turn a pointer position into a slot without touching row elements. */
export interface RowGeometry {
  rowH: number
  count: number
  /** Viewport y of the top of the first row (it moves as the page scrolls). */
  bodyTop: () => number
  /** The visible part of the scroller, in viewport coordinates, below the sticky header. */
  frame: () => FrameRect
  scrollBy: (dy: number) => void
  /** Rows that fit in one viewport, for PageUp / PageDown. */
  page: () => number
}

interface Options {
  enabled: boolean
  count: number
  /** The table has its own scroll box (an explicit maxHeight); otherwise the nearest scrolling ancestor, or the page. */
  ownScroller: boolean
  frameRef: RefObject<HTMLElement | null>
  bodyRef: RefObject<HTMLTableSectionElement | null>
  headRef: RefObject<HTMLTableSectionElement | null>
}

// Before any row has been measured: the compact density's 2.25rem.
const DEFAULT_ROW_H = 36
// Used until the scroller reports a height (it has none before layout, and none in jsdom).
const FALLBACK_VIEWPORT = 800

/**
 * Windowing state for DataTable: which rows to draw, the row pitch, and the scroll
 * geometry. Rows have ONE fixed pitch, measured from two adjacent drawn rows (so it
 * follows the density setting and includes the border), and everything else is
 * arithmetic on that, see windowing.ts. The scroller is, in order: the table's own
 * box when it has an explicit height, the nearest ancestor that scrolls (the app
 * shell's main area), or the document.
 */
export function useWindowedRows({ enabled, count, ownScroller, frameRef, bodyRef, headRef }: Options) {
  const [range, setRange] = useState({ start: 0, end: 0, first: 0 })
  const [rowH, setRowH] = useState(DEFAULT_ROW_H)
  const [kind, setKind] = useState<'window' | 'element'>('window')
  const scroller = useRef<HTMLElement | null>(null)
  const rowHRef = useRef(rowH)
  rowHRef.current = rowH
  const countRef = useRef(count)
  countRef.current = count

  const viewport = useCallback((): FrameRect => {
    const el = scroller.current
    if (el) {
      const b = el.getBoundingClientRect()
      return { top: b.top, bottom: b.bottom, left: b.left, right: b.right, width: b.width, height: b.height || el.clientHeight }
    }
    return { top: 0, left: 0, right: window.innerWidth, bottom: window.innerHeight, width: window.innerWidth, height: window.innerHeight }
  }, [])
  const scrollTopNow = () => (scroller.current ? scroller.current.scrollTop : window.scrollY)
  const setScrollTop = (y: number) => { if (scroller.current) scroller.current.scrollTop = y; else window.scrollTo(0, y) }
  // Header height plus whatever the header is stuck below (the shell bar): rows under
  // that band are covered, so reveal and auto-scroll treat it as outside the viewport.
  const topInset = useCallback(() => {
    const head = headRef.current
    if (!head) return 0
    return head.getBoundingClientRect().height + (parseFloat(getComputedStyle(head).top) || 0)
  }, [headRef])

  const measure = useCallback(() => {
    const body = bodyRef.current
    if (!body) return
    const vp = viewport()
    const offset = vp.top - body.getBoundingClientRect().top
    const next = windowRange({
      offset,
      viewport: vp.height > 0 ? vp.height : FALLBACK_VIEWPORT,
      rowH: rowHRef.current,
      count: countRef.current,
      overscan: OVERSCAN,
    })
    const first = rowIndexAtOffset(offset + topInset(), rowHRef.current, countRef.current)
    setRange(prev => (prev.start === next.start && prev.end === next.end && prev.first === first ? prev : { ...next, first }))
  }, [bodyRef, viewport, topInset])

  // The pitch over the first run of ADJACENT drawn rows (a spacer between rows would be in
  // the gap), measured from the second row on: see runPitch.
  const measurePitch = useCallback(() => {
    const rows = bodyRef.current?.querySelectorAll<HTMLElement>('[data-row]')
    if (!rows) return
    const run: number[] = []
    for (let i = 0; i < rows.length && run.length < 12; i++) {
      if (i > 0 && rows[i - 1].nextElementSibling !== rows[i]) { if (run.length >= 3) break; run.length = 0 }
      run.push(rows[i].getBoundingClientRect().top)
    }
    const pitch = runPitch(run)
    if (pitch !== null && Math.abs(pitch - rowHRef.current) > 0.05) setRowH(pitch)
  }, [bodyRef])

  useLayoutEffect(() => {
    if (!enabled) return
    const frame = frameRef.current
    if (!frame) return
    let el: HTMLElement | null = null
    if (ownScroller) el = frame
    else {
      for (let p = frame.parentElement; p && p !== document.body && p !== document.documentElement; p = p.parentElement) {
        const oy = getComputedStyle(p).overflowY
        if (oy === 'auto' || oy === 'scroll' || oy === 'overlay') { el = p; break }
      }
    }
    scroller.current = el
    setKind(el ? 'element' : 'window')

    // Scroll events arrive faster than frames; a frame is the finest the window needs.
    let raf = 0
    const schedule = () => { if (!raf) raf = requestAnimationFrame(() => { raf = 0; measure() }) }
    const target: HTMLElement | Window = el ?? window
    target.addEventListener('scroll', schedule, { passive: true })
    window.addEventListener('resize', schedule)
    // A width change reflows nothing here, but a height change (density, a banner above
    // the table) moves the rows; the body observer also catches the pitch changing.
    let ro: ResizeObserver | undefined
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(() => { measurePitch(); schedule() })
      ro.observe(frame)
      if (el) ro.observe(el)
      if (bodyRef.current) ro.observe(bodyRef.current)
    }
    measurePitch()
    measure()
    return () => {
      target.removeEventListener('scroll', schedule)
      window.removeEventListener('resize', schedule)
      ro?.disconnect()
      if (raf) cancelAnimationFrame(raf)
    }
  }, [enabled, ownScroller, frameRef, bodyRef, measure, measurePitch])

  // Rows added or removed, or a new pitch: recompute the window now, before paint.
  useLayoutEffect(() => { if (enabled) { measurePitch(); measure() } }, [enabled, count, rowH, measure, measurePitch])

  // The first rows are drawn only after the first measure(), and the pitch can be read only
  // from drawn rows: so read it again whenever the window changes (it settles at once).
  useLayoutEffect(() => { if (enabled) measurePitch() }, [enabled, range.start, range.end, measurePitch])

  const scrollToIndex = useCallback((index: number, align: 'nearest' | 'center' = 'nearest'): boolean => {
    const body = bodyRef.current
    if (!body || index < 0 || index >= countRef.current) return false
    const vp = viewport()
    const st = scrollTopNow()
    const rowTop = body.getBoundingClientRect().top - vp.top + st + index * rowHRef.current
    const target = revealScrollTop({ rowTop, rowH: rowHRef.current, scrollTop: st, viewport: vp.height, topInset: topInset(), align })
    if (target !== st) setScrollTop(target)
    measure()
    return true
  }, [bodyRef, viewport, topInset, measure])

  const geometry = useMemo<RowGeometry>(() => ({
    rowH,
    count,
    bodyTop: () => bodyRef.current?.getBoundingClientRect().top ?? 0,
    frame: () => { const v = viewport(); return { ...v, top: v.top + topInset(), height: v.height - topInset() } },
    scrollBy: dy => setScrollTop(scrollTopNow() + dy),
    page: () => pageRows(viewport().height, topInset(), rowHRef.current),
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [rowH, count, bodyRef, viewport, topInset])

  /** `first` is the first row under the sticky header, for the default tab stop. */
  return { range, first: range.first, rowH, kind, geometry, scrollToIndex, measure }
}
