import { useTranslation } from 'react-i18next'

import type { DestParseReport } from '@/api/accessControl'
import { IconWarning } from '@/lib/icons'
import { reasonText, warningText } from './listStatus'

/** At most this many ignored lines are listed; the rest are a count. */
const MAX_LISTED = 20

const ROW = 'grid grid-cols-1 gap-x-2 sm:grid-cols-[5.5rem_minmax(0,1fr)_minmax(0,1.2fr)]'

/**
 * WHAT THE LIST WAS UNDERSTOOD AS: how many entries were kept, rewritten and
 * left out, and why, line by line. A line number jumps the editor to it.
 * Warnings carry the warning icon and never block the save.
 */
export default function ParseReport({ report, onJump }: { report: DestParseReport; onJump?: (line: number) => void }) {
  const { t } = useTranslation(['admin'])
  const P = 'admin:access_control.lists.report.'
  const samples = report.samples ?? []
  const warnings = report.warnings ?? []
  const listed = samples.slice(0, MAX_LISTED)
  const more = Math.max(0, report.ignored + report.rewritten - listed.length)
  const lineLink = (line: number) => onJump
    ? (
      <button type="button" aria-label={t(`${P}jump`, { line })} onClick={() => onJump(line)}
        className="tabular-nums underline-offset-2 outline-none hover:underline focus-visible:underline">
        {t('admin:access_control.lists.dialog.line_n', { line })}
      </button>
    )
    : t('admin:access_control.lists.dialog.line_n', { line })
  return (
    <div data-testid="parse-report" className="rounded-lg bg-muted p-3 text-sm">
      <p className="font-medium">
        {t(`${P}summary`, { accepted: report.accepted, rewritten: report.rewritten, ignored: report.ignored })}
        {report.duplicates > 0 ? ` · ${t(`${P}duplicates`, { count: report.duplicates })}` : ''}
      </p>
      {listed.map(s => (
        <div key={`${s.line}:${s.reason}`} className={`${ROW} mt-1.5`}>
          <span>{lineLink(s.line)}</span>
          <span className="font-mono text-[13px] [overflow-wrap:anywhere]">{s.text}</span>
          <span className="text-muted-foreground">{reasonText(t, s.reason)}</span>
        </div>
      ))}
      {more > 0 && <p className="mt-1.5 text-muted-foreground">{t(`${P}more`, { count: more })}</p>}
      {warnings.length > 0 && (
        <div className="mt-2 rounded-md bg-warning/12 p-2">
          <p className="flex items-center gap-1 text-[12.5px] font-medium"><IconWarning aria-hidden className="size-3.5 text-warning-ink" />{t(`${P}warnings`)}</p>
          {warnings.map(w => (
            <div key={`${w.line}:${w.code}`} className={`${ROW} mt-1`}>
              <span>{lineLink(w.line)}</span>
              <span className="font-mono text-[13px] [overflow-wrap:anywhere]">{w.text}</span>
              <span>{warningText(t, w.code)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
