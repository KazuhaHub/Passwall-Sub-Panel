import type { KeyboardEvent, RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { Redo2Icon, Undo2Icon } from 'lucide-react'

import { IconButton } from '@/components/common/IconButton'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { IconFilter, IconSearch, IconWarning } from '@/lib/icons'
import { cn } from '@/lib/utils'
import type { ToolbarTier } from './layout'

type View = 'blocks' | 'text'

/**
 * The one row of the Rules tab (design 3.2), grouped on purpose and never left to wrap on its own:
 *
 *   left   find (search) · view (Groups | Text) · undo and redo · filter by target
 *   right  the counts · Problems and hints · Test routing
 *
 * At a narrower width things leave the row in a fixed order, so the toolbar looks the same at every width it has the room for:
 * `compact` drops the counts (the directory says them), `narrow` (a tablet, a phone) puts Problems and the target filter in a
 * More menu (the filter opens a second row, and stays open while a target is chosen), and gives the search its own row.
 */
export function RulesToolbar({ tier, view, textOnly, readOnly, query, onQuery, onSearchKey, searchRef, onView, history, dialogOpen, targets, targetFilter, onTargetFilter, targetMode, onTargetMode, stats, subRule, testerOpen, onToggleTester, onOpenIssues, filterRow, onFilterRow }: {
  tier: ToolbarTier
  view: View
  textOnly: boolean
  readOnly: boolean
  query: string
  onQuery: (next: string) => void
  onSearchKey: (e: KeyboardEvent<HTMLInputElement>) => void
  searchRef: RefObject<HTMLInputElement | null>
  onView: (next: View) => void
  history: { canUndo: boolean; canRedo: boolean; undo: () => void; redo: () => void }
  /** A dialog is open: undo would change the document under it. */
  dialogOpen: boolean
  targets: readonly string[]
  targetFilter: string
  onTargetFilter: (next: string) => void
  targetMode: 'merged' | 'runs'
  onTargetMode: (next: 'merged' | 'runs') => void
  stats: string
  subRule: boolean
  testerOpen: boolean
  onToggleTester: () => void
  onOpenIssues: () => void
  /** The second row with the target filter, asked for from the More menu (narrow only). */
  filterRow: boolean
  onFilterRow: (next: boolean) => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const blocks = view === 'blocks'
  const narrow = tier === 'narrow'
  const canFilter = blocks && (targets.length > 1 || targetFilter !== '')
  const filterShown = canFilter && (!narrow || filterRow || targetFilter !== '')

  const targetSelect = (
    <NativeSelect aria-label={t('admin:rules.search.by_target')} value={targetFilter} onChange={e => onTargetFilter(e.target.value)}>
      <option value="">{t('admin:rules.search.by_target')}</option>
      {targets.map(x => <option key={x} value={x}>{x}</option>)}
    </NativeSelect>
  )
  const viewOfTarget = targetFilter !== '' && (
    <SegmentedControl<'merged' | 'runs'> aria-label={t('admin:rules.targetview.label')} value={targetMode} onChange={onTargetMode}
      options={[{ value: 'merged', label: t('admin:rules.targetview.merged') }, { value: 'runs', label: t('admin:rules.targetview.runs') }]} />
  )

  const more: MoreMenuItem[] = []
  if (narrow) {
    more.push({ id: 'issues', label: t('admin:rules.issues.title'), icon: IconWarning, onSelect: onOpenIssues })
    if (blocks && targets.length > 1) more.push({ id: 'filter', label: t('admin:rules.search.by_target'), icon: IconFilter, onSelect: () => onFilterRow(true) })
  }

  return (
    <div className="flex flex-col gap-2">
      <div role="toolbar" aria-label={t('admin:rules.toolbar')} className="flex flex-wrap items-center gap-x-2 gap-y-2">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
          {blocks && (
            <div className={cn('relative min-w-0', narrow ? 'basis-full' : 'max-w-sm min-w-44 flex-1 basis-56')}>
              <IconSearch aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input ref={searchRef} value={query} className="pl-8" placeholder={t('admin:rules.search.placeholder')} aria-label={t('admin:rules.search.placeholder')}
                onChange={e => onQuery(e.target.value)} onKeyDown={onSearchKey} data-rules-search />
            </div>
          )}
          <SegmentedControl<View> aria-label={t('admin:rules.view.label')} value={view} onChange={onView}
            options={[{ value: 'blocks', label: t('admin:rules.view.blocks'), disabled: textOnly }, { value: 'text', label: t('admin:rules.view.text') }]} />
          {!readOnly && blocks && (
            <span className="flex items-center">
              <IconButton label={t('admin:rules.undo')} disabled={!history.canUndo || dialogOpen} onClick={history.undo}><Undo2Icon /></IconButton>
              <IconButton label={t('admin:rules.redo')} disabled={!history.canRedo || dialogOpen} onClick={history.redo}><Redo2Icon /></IconButton>
            </span>
          )}
          {filterShown && !narrow && (
            <div className={cn('flex min-w-0 items-center gap-2', tier === 'compact' ? 'w-40' : 'w-48')}>
              {targetSelect}
            </div>
          )}
          {blocks && !narrow && viewOfTarget}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {tier === 'full' && <span className="text-xs whitespace-nowrap text-muted-foreground tabular-nums">{stats}</span>}
          {!narrow && (
            <Button variant="outline" size="sm" onClick={onOpenIssues}>
              <IconWarning />
              {t('admin:rules.issues.title')}
            </Button>
          )}
          {!subRule && <Button variant={testerOpen ? 'secondary' : 'outline'} size="sm" aria-pressed={testerOpen} onClick={onToggleTester}>{t('admin:rules.tester.title')}</Button>}
          {more.length > 0 && <MoreMenu items={more} />}
        </div>
      </div>
      {filterShown && narrow && (
        <div className="flex flex-wrap items-center gap-2">
          <div className="w-full min-w-0 sm:w-56">{targetSelect}</div>
          {viewOfTarget}
        </div>
      )}
    </div>
  )
}
