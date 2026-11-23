import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/common/IconButton'
import { IconCopy, IconDownload, IconMore, IconClean, IconSpinner } from '@/lib/icons'
import type { DiagFormat } from './useDiagFormat'

/**
 * The page's "more" menu: copy or download everything it read, and clear the
 * statistics. The clear sits below a divider because it is the one action
 * here that changes anything: it restarts the window for every admin who
 * opens this page. Nothing else reads these counters (only the diagnostics
 * handler calls metrics.Take); the risk center and the node pages read the
 * database, which a clear does not touch.
 *
 * Built here rather than from the shared MoreMenu because two of its items
 * carry a sentence the shared one cannot hold: what an export contains, and
 * why a clear is unavailable right now.
 */
export default function ExportMenu({ fmt, onCopy, onDownload, onReset, resetting, resetDisabledReason }: {
  fmt: DiagFormat
  onCopy: () => void
  onDownload: () => void
  onReset: () => void
  /** A clear is in flight: the item stays disabled and shows its wait. */
  resetting: boolean
  /** Why clearing makes no sense right now, said under the item. */
  resetDisabledReason?: string
}) {
  const { t } = fmt
  const disabled = resetting || resetDisabledReason !== undefined
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<IconButton label={t('admin:diagnostics.actions.more')} size="icon-sm" aria-haspopup="menu" />}>
        <IconMore />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-auto min-w-52 max-w-xs">
        <DropdownMenuItem onClick={onCopy}><IconCopy />{t('admin:diagnostics.actions.copy_json')}</DropdownMenuItem>
        <DropdownMenuItem onClick={onDownload}><IconDownload />{t('admin:diagnostics.actions.download_json')}</DropdownMenuItem>
        <p role="note" className="px-2 pt-1 pb-1.5 text-xs whitespace-normal text-muted-foreground">{t('admin:diagnostics.actions.export_note')}</p>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" disabled={disabled} aria-busy={resetting || undefined} onClick={onReset}>
          {resetting ? <IconSpinner className="animate-spin" /> : <IconClean />}{t('admin:diagnostics.actions.reset')}
        </DropdownMenuItem>
        {!resetting && resetDisabledReason && (
          <p role="note" className="px-2 pt-1 pb-1.5 text-xs whitespace-normal text-muted-foreground">{resetDisabledReason}</p>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
