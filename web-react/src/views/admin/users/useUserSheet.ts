import { useCallback, useEffect, useRef } from 'react'
import { useUrlWriter } from '@/hooks/urlBatch'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { useUrlSelection } from '@/hooks/useUrlSelection'
import { parseRowId } from '../logs/shared'

/** `new` is the create form in the same sheet. */
export type SheetTarget = number | 'new'
export const SHEET_TABS = ['overview', 'settings', 'usage', 'activity', 'security'] as const
export type SheetTab = (typeof SHEET_TABS)[number]

const parse = (raw: string | null): SheetTarget | null => (raw === 'new' ? 'new' : parseRowId(raw))

/**
 * The opened user (`?user=<id>`, or `?user=new`) and the tab it is on (`?tab=`) are in the URL: a user is a
 * link, Back closes the sheet, and the palette's `/admin/users?user=7` opens one. Opening PUSHES the first time
 * (Back closes it) and moving between users replaces; closing drops both parameters in one navigation.
 */
export function useUserSheet() {
  const sel = useUrlSelection<SheetTarget>('user', { parse })
  const [tab, setTab] = useUrlState('tab', urlCodecs.oneOf(SHEET_TABS, 'overview'))
  const write = useUrlWriter()

  const open = useCallback((target: SheetTarget, to?: SheetTab) => {
    write((params, ctx) => {
      if (params.get('user') === null) ctx.push()
      params.set('user', String(target))
      if (to && to !== 'overview') params.set('tab', to); else params.delete('tab')
    }, 'replace')
  }, [write])

  const close = useCallback(() => write(params => { params.delete('user'); params.delete('tab') }, 'replace'), [write])

  // The two ways out of a sheet that must not ask about an unsaved draft: the draft is moot once the user is
  // deleted or the create finished. `bypass` is read by the sheet's leave guard and cleared once the sheet is gone.
  const bypass = useRef(false)
  const closed = sel.selected === null
  useEffect(() => { if (closed) bypass.current = false }, [closed])
  const exit = useCallback(() => { bypass.current = true; close() }, [close])

  return { target: sel.selected, tab, setTab, open, close, exit, bypass }
}

export type UserSheetControl = ReturnType<typeof useUserSheet>
