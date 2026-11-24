import { useState } from 'react'
import type { ReconcileIssue, ReconcileReport } from '@/api/reconcile'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useAdminT } from './useAdminT'

function IssueList({ title, issues, tone }: { title: string; issues: ReconcileIssue[]; tone?: 'danger' }) {
  if (issues.length === 0) return null
  return (
    <div>
      <h3 className={tone === 'danger' ? 'text-[0.8125rem] font-medium text-destructive' : 'text-[0.8125rem] font-medium'}>{title}（{issues.length}）</h3>
      <ul className="mt-1 flex list-disc flex-col gap-1 pl-5 text-xs text-muted-foreground">
        {issues.map((i, idx) => (
          <li key={idx} className="break-words">
            {i.panel_name && <strong className="font-medium text-foreground">[{i.panel_name}] </strong>}{i.client_email || '(node)'}
            {' — '}<code className="font-mono">{i.code}</code>{i.detail ? `: ${i.detail}` : ''}
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * What a reconcile run found: how many it scanned and fixed, and on request each issue, split into what the run
 * healed and what it could not. Shown whenever something happened (a fix or an issue left), not only for leftovers.
 */
export function ReconcileDialog({ report, onClose }: { report: ReconcileReport; onClose: () => void }) {
  const t = useAdminT()
  // Collapsed: the summary is the answer, the per-issue list is for whoever wants it.
  const [details, setDetails] = useState(false)
  const issues = report.issues ?? []
  return (
    <Dialog open onOpenChange={open => { if (!open) onClose() }}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader><DialogTitle>{t('users.reconcile.result_title')}</DialogTitle></DialogHeader>
        <p className="text-sm">
          {report.fixed > 0
            ? t('users.reconcile.summary_fixed', { scanned: report.scanned, fixed: report.fixed })
            : t('users.reconcile.summary_no_fix', { scanned: report.scanned })}
        </p>
        {details && issues.length > 0 && (
          <div className="flex max-h-80 flex-col gap-3 overflow-y-auto">
            <IssueList title={t('users.reconcile.fixed_section')} issues={issues.filter(i => i.fixed)} />
            <IssueList title={t('users.reconcile.unfixed_section')} issues={issues.filter(i => !i.fixed)} tone="danger" />
          </div>
        )}
        <DialogFooter>
          {issues.length > 0 && <Button variant="outline" onClick={() => setDetails(v => !v)}>{t(details ? 'users.reconcile.hide_details' : 'users.reconcile.show_details')}</Button>}
          <Button onClick={onClose}>{t('common:actions.ok')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
