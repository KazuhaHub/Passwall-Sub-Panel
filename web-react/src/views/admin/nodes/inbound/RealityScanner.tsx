import { useCallback, useEffect, useState } from 'react'
import { scanRealityTargets, type RealityScanResponse, type RealityScanResult } from '@/api/nodes'
import { StatusBadge } from '@/components/common/StatusBadge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useSpacedT } from '@/i18n/spacing'
import { IconSearch, IconSpinner } from '@/lib/icons'
import { useNodesT } from '../shared/useNodesT'

/**
 * REALITY target discovery. Every scan is deliberately routed through the selected server, never probed
 * from the browser or from PSP's own host; the source label names which server runs it, so the network
 * vantage the results were measured from is never a guess.
 *
 * Opening it starts a scan with the common candidates. A failed scan clears the list, so stale rows can
 * never be taken for fresh output (the request layer owns the error toast).
 */
export function RealityScanner({ open, panelId, sourceName, onClose, onUse }: {
  open: boolean
  panelId: number
  /** The name of the server the scan runs on, shown until the response names it. */
  sourceName?: string
  onClose: () => void
  onUse: (result: RealityScanResult) => void
}) {
  const t = useNodesT()
  const { t: tc } = useSpacedT()
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [response, setResponse] = useState<RealityScanResponse | null>(null)

  const run = useCallback(async (targets?: string) => {
    if (panelId <= 0) return
    setLoading(true)
    try {
      setResponse(await scanRealityTargets(panelId, targets))
    } catch {
      setResponse(null)
    } finally {
      setLoading(false)
    }
  }, [panelId])

  useEffect(() => {
    if (!open) return
    setQuery('')
    setResponse(null)
    void run()
  }, [open, run])

  const source = response?.source_panel_name || sourceName || `#${panelId}`
  const items = response?.items ?? []
  const scan = () => void run(query.trim() || undefined)

  return (
    <Dialog open={open} onOpenChange={next => { if (!next) onClose() }}>
      <DialogContent showCloseButton={false} className="sm:max-w-5xl">
        <DialogHeader>
          <div className="flex flex-wrap items-center gap-2">
            <DialogTitle>{t('nodes.create_dialog.reality_scan_title')}</DialogTitle>
            <Badge variant="outline">{t('nodes.create_dialog.reality_scan_source', { source })}</Badge>
          </div>
          <DialogDescription>{t('nodes.create_dialog.reality_scan_desc')}</DialogDescription>
        </DialogHeader>
        <div className="flex items-start gap-2">
          <Input value={query} placeholder={t('nodes.create_dialog.reality_scan_placeholder')} aria-label={t('nodes.create_dialog.reality_scan_target')}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); scan() } }} />
          <Button type="button" disabled={loading} onClick={scan}>
            {loading ? <IconSpinner aria-hidden className="animate-spin" /> : <IconSearch aria-hidden />}
            {t('nodes.create_dialog.reality_scan')}
          </Button>
        </div>
        <div className="max-h-[26rem] overflow-y-auto rounded-lg border">
          <Table className="min-w-[56rem]">
            <TableHeader>
              <TableRow>
                <TableHead>{t('nodes.create_dialog.reality_scan_target')}</TableHead>
                <TableHead>{t('nodes.create_dialog.reality_scan_status')}</TableHead>
                <TableHead>TLS</TableHead>
                <TableHead>ALPN</TableHead>
                <TableHead>{t('nodes.create_dialog.reality_scan_key_exchange')}</TableHead>
                <TableHead>{t('nodes.create_dialog.reality_scan_certificate')}</TableHead>
                <TableHead className="text-right">{t('nodes.create_dialog.reality_scan_latency')}</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && items.length === 0 ? (
                <TableRow><TableCell colSpan={8} className="py-8 text-center"><IconSpinner aria-hidden className="mx-auto animate-spin" /></TableCell></TableRow>
              ) : items.length === 0 ? (
                <TableRow><TableCell colSpan={8} className="py-8 text-center text-muted-foreground">{t('nodes.create_dialog.reality_scan_empty')}</TableCell></TableRow>
              ) : items.map(row => (
                <TableRow key={row.target}>
                  <TableCell title={row.ip ? `${row.target} — ${row.ip}` : row.target}>
                    <div>{row.target}</div>
                    {row.ip && <div className="text-xs text-muted-foreground">{row.ip}</div>}
                  </TableCell>
                  <TableCell title={row.feasible ? undefined : row.reason || t('nodes.create_dialog.reality_scan_not_feasible')}>
                    <StatusBadge quiet={false} tone={row.feasible ? 'success' : 'warning'}
                      label={row.feasible ? t('nodes.create_dialog.reality_scan_feasible') : t('nodes.create_dialog.reality_scan_not_feasible')} />
                  </TableCell>
                  <TableCell>{row.tlsVersion || '—'}</TableCell>
                  <TableCell>{row.alpn || '—'}</TableCell>
                  <TableCell>{row.curveID || '—'}</TableCell>
                  <TableCell>
                    {row.certValid
                      ? <span title={[row.certSubject, row.certIssuer].filter(Boolean).join(' — ')} className="block max-w-44 truncate">{row.certSubject || '—'}</span>
                      : <span className="text-xs text-muted-foreground">{t('nodes.create_dialog.reality_scan_cert_invalid')}</span>}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{row.latencyMs > 0 ? `${row.latencyMs} ms` : '—'}</TableCell>
                  <TableCell className="text-right">
                    <Button type="button" size="sm" variant="outline" onClick={() => { onUse(row); onClose() }}>{t('nodes.create_dialog.reality_scan_use')}</Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={loading} onClick={scan}>{t('nodes.create_dialog.reality_scan_rescan')}</Button>
          <Button type="button" onClick={onClose}>{tc('actions.close')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
