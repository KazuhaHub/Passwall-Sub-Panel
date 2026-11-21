import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { LEGAL_CONTENT_MAX_BYTES, publishLegal, type LegalKind } from '@/api/legal'
import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { OptionCards } from '@/components/form/OptionCards'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { legalKeys } from '@/query/keys'
import { useAffectedUsers } from '@/query/legal'
import { useQueryScope } from '@/query/useQueryScope'
import { Notice } from '../Notice'
import { useAdminT } from '../useAdminT'

/** How many characters were added to and removed from `from` to make `to`: what
 *  is left after trimming the text they share at both ends. A reading aid for
 *  the confirmation, not a diff. */
export function changeSize(from: string, to: string): { added: number; removed: number } {
  const a = Array.from(from)
  const b = Array.from(to)
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++
  return { added: b.length - head - tail, removed: a.length - head - tail }
}

interface Props {
  kind: LegalKind
  locale: string
  /** The version this publication will be (the server numbers it; this is the one it expects). */
  version: number
  /** The text now published in this locale, null when there is none. */
  previous: string | null
  content: string
  onClose: () => void
  onPublished: () => void
}

/**
 * The confirmation in front of a publication. Asking every user to accept again is the one consequence that
 * reaches beyond this page, so it is a choice of its own with its effect written out (how many people, when
 * they see it), and the button says it when it is chosen. A minor change is the default: a typo fix must never
 * interrupt anyone by accident.
 */
export default function LegalPublishDialog({ kind, locale, version, previous, content, onClose, onPublished }: Props) {
  const t = useAdminT()
  const scope = useQueryScope()
  const qc = useQueryClient()
  const affected = useAffectedUsers(scope, true)
  const [bump, setBump] = useState(false)
  const [error, setError] = useState('')

  const doc = t(`auth:legal.kind.${kind}`)
  const size = previous === null ? null : changeSize(previous, content)
  const users = affected.data ? affected.data.affected_users.toLocaleString('en-US') : null

  async function publish() {
    setError('')
    try {
      const res = await publishLegal(kind, { locale, content, consent_bump: bump })
      pushSnack(t('admin:settings.legal.publish.done', { version: res.document.version }), 'success')
      await qc.invalidateQueries({ queryKey: legalKeys.all(scope) })
      onPublished()
    } catch (err) {
      const res = (err as { response?: { status?: number; data?: { code?: string } } }).response
      if (res?.status === 409) {
        // Somebody else took the version number: show what they published, keep the draft, and let the administrator decide.
        pushSnack(t('admin:settings.legal.publish.conflict'), 'warning')
        await qc.invalidateQueries({ queryKey: legalKeys.all(scope) })
        onClose()
      } else if (res?.data?.code === 'legal_content_too_large') {
        setError(t('admin:settings.legal.publish.too_large', { max: LEGAL_CONTENT_MAX_BYTES.toLocaleString('en-US') }))
      } else {
        setError(t('admin:settings.legal.publish.failed'))
      }
    }
  }

  return (
    <Dialog open onOpenChange={o => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader className="pr-8">
          <DialogTitle>{t('admin:settings.legal.publish.title', { doc, locale, version })}</DialogTitle>
          <DialogDescription>{size ? t('admin:settings.legal.publish.diff', size) : t('admin:settings.legal.publish.first')}</DialogDescription>
        </DialogHeader>
        <OptionCards
          aria-label={t('admin:settings.legal.publish.kind_label')}
          columns={1}
          value={bump ? 'major' : 'minor'}
          onChange={v => setBump(v === 'major')}
          options={[
            { value: 'minor', title: t('admin:settings.legal.publish.minor'), description: t('admin:settings.legal.publish.minor_hint') },
            {
              value: 'major', title: t('admin:settings.legal.publish.bump'),
              description: users !== null ? t('admin:settings.legal.publish.bump_hint', { users }) : t('admin:settings.legal.publish.bump_hint_loading'),
            },
          ]}
        />
        {bump && <Notice>{t('admin:settings.legal.publish.bump_effect')}</Notice>}
        {error && <Notice tone="danger" role="alert">{error}</Notice>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('admin:settings.legal.publish.cancel')}</Button>
          <AsyncButton variant={bump ? 'destructive' : 'default'} onClick={publish}>
            {bump ? t('admin:settings.legal.publish.confirm_bump') : t('admin:settings.legal.publish.confirm')}
          </AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
