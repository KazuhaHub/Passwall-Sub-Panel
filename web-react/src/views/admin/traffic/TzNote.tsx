import { useSpacedT } from '@/i18n/spacing'
import { useSiteStore } from '@/stores/site'
import { cn } from '@/lib/utils'

/**
 * A one-line caption saying that nearby times are in the PANEL time zone, naming the viewer's own
 * zone too. It renders nothing when the two are the same, so the common single-zone case stays
 * quiet. Used beside a panel-zone chart or table, where a date axis cannot carry a zone per label.
 */
export function TzNote({ className }: { className?: string }) {
  const { t } = useSpacedT()
  const panelTz = useSiteStore(s => s.timezone)
  let browserTz = ''
  try {
    browserTz = Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    browserTz = ''
  }
  if (!panelTz || !browserTz || panelTz === browserTz) return null
  return (
    <p className={cn('text-xs text-muted-foreground', className)}>
      {t('tz_hint', { panel: panelTz, browser: browserTz, defaultValue: '时间按面板时区 {{panel}} 显示（你的本地：{{browser}}）' })}
    </p>
  )
}
