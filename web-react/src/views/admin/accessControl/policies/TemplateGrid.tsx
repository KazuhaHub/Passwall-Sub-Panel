import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { DestCatalog, DestState, DestTemplate, DestUnavailableTemplate } from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { AsyncButton } from '@/components/common/AsyncButton'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { pickLocalized } from './templates'

const T = 'admin:access_control.templates.'

const CARD = 'flex min-w-0 flex-col gap-1.5 rounded-xl border bg-card p-3'

/**
 * The catalog as cards (S4): what a rule group blocks, in the server's own
 * words for the UI language. A group already added says so quietly and offers
 * "add another". The groups the catalog lists as unavailable are honest
 * cards: they say why there is no generic category and send the admin to
 * write a list of their own, instead of passing off a one-country category.
 */
export default function TemplateGrid({ catalog, onUse, onGoToLists }: {
  catalog: DestCatalog
  onUse: (tpl: DestTemplate) => void
  onGoToLists: () => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {catalog.templates.map(tpl => (
        <div key={tpl.key} className={CARD} data-testid="template-card">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-semibold">{pickLocalized(tpl.name, i18n.language)}</span>
            {tpl.added && <AccessBadge kind="none" label={t(`${T}added`)} />}
          </div>
          <p className="flex-1 text-sm text-muted-foreground">{pickLocalized(tpl.description, i18n.language)}</p>
          <div><Button size="sm" variant="outline" onClick={() => onUse(tpl)}>{t(tpl.added ? `${T}add_another` : `${T}use`)}</Button></div>
        </div>
      ))}
      {catalog.unavailable.map(u => <UnavailableCard key={u.key} item={u} onGoToLists={onGoToLists} />)}
    </div>
  )
}

function UnavailableCard({ item, onGoToLists, className }: { item: DestUnavailableTemplate; onGoToLists: () => void; className?: string }) {
  const { t, i18n } = useTranslation(['admin'])
  return (
    <div className={cn(CARD, 'border-dashed', className)} data-testid="template-unavailable">
      <span className="text-sm font-semibold">{pickLocalized(item.name, i18n.language)}</span>
      <p className="flex-1 text-sm text-muted-foreground">{pickLocalized(item.reason, i18n.language)}</p>
      <div><Button size="sm" variant="outline" onClick={onGoToLists}>{t(`${T}new_list`)}</Button></div>
    </div>
  )
}

/** The groups without a generic category, from the menu's footer. */
export function UnavailableDialog({ open, catalog, onClose, onGoToLists }: {
  open: boolean
  catalog: DestCatalog | undefined
  onClose: () => void
  onGoToLists: () => void
}) {
  const { t } = useTranslation(['admin'])
  return (
    <Dialog open={open} onOpenChange={next => { if (!next) onClose() }}>
      <DialogContent showCloseButton={false} className="sm:max-w-lg">
        <DialogHeader><DialogTitle>{t(`${T}unavailable_title`)}</DialogTitle></DialogHeader>
        <div className="grid gap-3">
          {(catalog?.unavailable ?? []).map(u => <UnavailableCard key={u.key} item={u} onGoToLists={() => { onClose(); onGoToLists() }} />)}
        </div>
        <DialogFooter><Button variant="outline" onClick={onClose}>{t(`${T}close`)}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Adding a rule group asks for its state: nothing is enabled by default, so
 * the admin says Off, Report only or On here (and nothing is chosen until they do).
 */
export function TemplateChooser({ template, onClose, onConfirm, error, onOpenSettings }: {
  template: DestTemplate | null
  onClose: () => void
  onConfirm: (state: DestState) => Promise<void>
  /** A refusal to show in the dialog (community data not downloaded, ...). */
  error: { text: string; needsData: boolean } | null
  onOpenSettings: () => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const [state, setState] = useState<DestState | null>(null)
  // A fresh choice every time it opens: nothing is chosen until the admin says.
  useEffect(() => { if (template === null) setState(null) }, [template])
  return (
    <Dialog open={template !== null} onOpenChange={next => { if (!next) onClose() }}>
      <DialogContent showCloseButton={false} className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(`${T}chooser_title`, { name: template ? pickLocalized(template.name, i18n.language) : '' })}</DialogTitle>
          <DialogDescription>{template ? pickLocalized(template.description, i18n.language) : ''}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <span id="template-state-label" className="text-sm font-semibold">{t(`${T}chooser_state`)}</span>
          {/* The templates are block policies: Report only is offered and recommended, never preselected. */}
          <SegmentedControl aria-labelledby="template-state-label" value={state ?? ('' as DestState)} onChange={setState}
            options={[
              { value: 'off', label: t('admin:access_control.policies.state.off') },
              { value: 'report', label: <>{t('admin:access_control.policies.state.report')}<span className="text-[11px] font-semibold text-primary">{t(`${T}recommend`)}</span></> },
              { value: 'on', label: t('admin:access_control.policies.state.on') },
            ]} />
          <p className="text-xs text-muted-foreground">{t(`${T}chooser_help`)}</p>
          {template?.counts_as_risk && <p className="text-xs text-muted-foreground">{t(`${T}chooser_risk`)}</p>}
          {error && (
            <div role="alert" className="flex flex-wrap items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-sm">
              <span className="min-w-0 flex-1">{error.text}</span>
              {error.needsData && <Button size="sm" variant="outline" onClick={onOpenSettings}>{t(`${T}update_data`)}</Button>}
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t(`${T}cancel`)}</Button>
          <AsyncButton disabled={state === null} onClick={async () => { if (state) await onConfirm(state) }}>{t(`${T}add`)}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
