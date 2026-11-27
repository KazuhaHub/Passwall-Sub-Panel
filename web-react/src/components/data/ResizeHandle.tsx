import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { useSpacedT } from '@/i18n/spacing'
import { cn } from '@/lib/utils'
import { MIN_COLUMN_WIDTH } from './columnWidths'

const STEP = 16
const BIG_STEP = 64

interface Props {
  /** The column's name, for the accessible name "Resize column <name>". */
  name: string
  /** The width the column has right now (what a drag or a key press starts from). */
  getWidth: () => number
  /** The widest this column may become, given the others. */
  getMax: () => number
  /** `width` is the new width in px; it is already clamped to [min, max] by the caller's clamp. */
  onResize: (width: number) => void
  /** A gesture is over (pointer released, key pressed): time to save. */
  onCommit: () => void
  /** Back to the fitted width. */
  onReset: () => void
  /** False when the column cannot get wider (a table that cannot scroll sideways is full): the cursor then says it only goes left. */
  canGrow: boolean
  /** Shown to assistive technology; undefined while the table has not been measured yet. */
  width: number | undefined
}

/**
 * The drag edge of a column header. A focusable `separator` is ARIA's "splitter" widget: it takes the arrow
 * keys (left/right by 16px, Shift by 64px), exposes its value, and a double click or the Home key gives the column
 * back its fitted width (the handle's description says so, for the tables that have no Columns menu to reset from). The pointer is tracked on the window while the button is down, so a drag that leaves the
 * header (or the table) keeps going, and it is cancelled by `pointercancel` like a release.
 */
export function ResizeHandle({ name, getWidth, getMax, onResize, onCommit, onReset, canGrow, width }: Props) {
  const { t } = useSpacedT()
  const hintId = useId()
  const [active, setActive] = useState(false)
  const [measured, setMeasured] = useState<number | undefined>(undefined)
  const stop = useRef<(() => void) | null>(null)
  const self = useRef<HTMLDivElement>(null)
  // A drag still running when the table goes away must not leave listeners or a text-selection lock behind.
  useEffect(() => () => { stop.current?.() }, [])

  // A focused handle with no known width (a table still in its natural layout, just after a reset) reads it from the layout.
  useLayoutEffect(() => {
    if (width !== undefined || self.current !== document.activeElement) return
    const w = Math.round(getWidth())
    if (w > 0 && w !== measured) setMeasured(w)
  }, [width, measured, getWidth])

  const clamp = (w: number) => Math.round(Math.min(Math.max(w, MIN_COLUMN_WIDTH), Math.max(getMax(), MIN_COLUMN_WIDTH)))

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    e.preventDefault()
    // The header's own click (sort) and the row's click (open) are not wanted for a resize.
    e.stopPropagation()
    const startX = e.clientX
    const startW = getWidth()
    let moved = false
    const move = (ev: Event) => {
      const dx = (ev as unknown as { clientX: number }).clientX - startX
      if (dx === 0 && !moved) return
      moved = true
      const want = startW + dx
      const got = clamp(want)
      // At the limit the cursor says so: the column cannot get any wider here.
      document.body.style.cursor = want > got ? 'not-allowed' : 'col-resize'
      onResize(got)
    }
    const end = () => {
      stop.current?.()
      if (moved) onCommit()
    }
    const prevSelect = document.body.style.userSelect
    const prevCursor = document.body.style.cursor
    document.body.style.userSelect = 'none'
    document.body.style.cursor = 'col-resize'
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', end)
    window.addEventListener('pointercancel', end)
    setActive(true)
    stop.current = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', end)
      window.removeEventListener('pointercancel', end)
      document.body.style.userSelect = prevSelect
      document.body.style.cursor = prevCursor
      setActive(false)
      stop.current = null
    }
  }

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Home') { e.preventDefault(); e.stopPropagation(); onReset(); return }
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
    e.preventDefault()
    e.stopPropagation()
    const step = e.shiftKey ? BIG_STEP : STEP
    const next = clamp(getWidth() + (e.key === 'ArrowRight' ? step : -step))
    onResize(next)
    setMeasured(next)
    onCommit()
  }

  const now = width ?? measured
  return (
    <>
    <span id={hintId} className="sr-only">{t('ui.data.resize_hint')}</span>
    <div
      ref={self}
      role="separator"
      aria-orientation="vertical"
      aria-label={t('ui.data.resize_column', { name })}
      aria-valuenow={now === undefined ? undefined : Math.round(now)}
      aria-describedby={hintId}
      aria-valuemin={MIN_COLUMN_WIDTH}
      aria-valuemax={Math.round(Math.max(getMax(), MIN_COLUMN_WIDTH))}
      tabIndex={0}
      data-no-row-click=""
      data-active={active ? 'true' : undefined}
      data-limit={canGrow ? undefined : 'true'}
      onFocus={() => setMeasured(Math.round(getWidth()))}
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      onClick={e => e.stopPropagation()}
      onDoubleClick={e => { e.stopPropagation(); onReset() }}
      // A 1px rule inside a wider hit area (wider again for a finger); it shows on hover, focus and while dragging.
      className={cn(
        'group/resize absolute inset-y-0 right-0 z-10 flex w-2 cursor-col-resize data-[limit=true]:cursor-w-resize touch-none justify-end outline-none select-none pointer-coarse:w-4',
        'after:h-full after:w-px after:bg-transparent after:transition-colors hover:after:bg-foreground/40 focus-visible:after:w-0.5 focus-visible:after:bg-ring data-[active=true]:after:w-0.5 data-[active=true]:after:bg-brand',
      )}
    />
    </>
  )
}
