import { useState } from 'react'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Field } from '@/components/form/Field'
import { NumberField } from '@/components/form/NumberField'
import { pushSnack } from '@/components/feedback/toasts'
import { getUISettings, putUISettings, type UISettings } from '@/api/settings'
import { IconTime } from '@/lib/icons'
import { useCan } from '@/utils/permissions'
import { useAdminT } from './shared'

type RetentionField = 'sub_log_retention_days' | 'mail_sent_retention_days'

/**
 * How long a log is kept, edited where the log is read (admin only: it is a setting). It writes the
 * same way the settings page does, a read of the whole settings object then a put of it with this one
 * field changed, so nothing else in it is touched. The current value is read when the popover opens, not
 * on every visit to the page.
 */
export function RetentionPopover({ field, label }: { field: RetentionField; label: string }) {
  const t = useAdminT()
  const admin = useCan('config.write')
  const [open, setOpen] = useState(false)
  const [saved, setSaved] = useState<number | null>(null)
  const [days, setDays] = useState<number | null>(null)
  const [failed, setFailed] = useState(false)
  if (!admin) return null

  async function onOpenChange(next: boolean) {
    setOpen(next)
    if (!next) return
    setFailed(false)
    try {
      const s = await getUISettings()
      setSaved(s[field])
      setDays(s[field])
    } catch { setFailed(true) /* the request layer toasts the cause */ }
  }

  async function save() {
    if (days === null) return
    const s: UISettings = await getUISettings()
    const updated = await putUISettings({ ...s, [field]: days })
    setSaved(updated[field])
    setDays(updated[field])
    pushSnack(t('logs.retention_saved'), 'success')
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={o => void onOpenChange(o)}>
      <PopoverTrigger render={<Button variant="outline" size="sm" />}>
        <IconTime />
        {t('logs.retention_open')}
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 gap-3">
        <Field label={label} hint={t('logs.retention_hint')}>
          {p => (
            <NumberField id={p.id} aria-describedby={p['aria-describedby']} value={days} onChange={setDays}
              unit={t('logs.retention_unit')} min={0} integer disabled={saved === null} />
          )}
        </Field>
        {failed && <p className="text-sm text-destructive">{t('logs.retention_load_failed')}</p>}
        <div className="flex justify-end">
          <AsyncButton disabled={days === null || days === saved} onClick={save}>{t('logs.retention_save')}</AsyncButton>
        </div>
      </PopoverContent>
    </Popover>
  )
}
