import { Field } from '@/components/form/Field'
import { NumberField } from '@/components/form/NumberField'
import { TONE_STYLE } from '@/components/common/tones'
import { IconWarning } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { deviceCapIsInert } from '@/utils/capabilities'
import type { GroupForm } from './model'
import { useGroupsT } from './useGroupsT'

/**
 * The group's entitlement policy: what members inherit unless they set their own. Each box can be EMPTY ("this
 * group states nothing") and that is not 0 ("unlimited, said out loud"): NumberField keeps the two apart and
 * the save sends a clear flag for the first and the number for the second (see model.updateRequest).
 */
export function LimitsTab({ form, setForm }: { form: GroupForm; setForm: (next: GroupForm) => void }) {
  const t = useGroupsT()
  const common = { allowEmpty: true, emptyLabel: t('admin:groups.limits.empty'), zeroLabel: t('admin:groups.limits.zero'), min: 0 } as const
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h3 className="text-sm font-medium">{t('admin:groups.field.limits_title')}</h3>
        <p className="text-xs text-muted-foreground">{t('admin:groups.field.limits_hint')}</p>
      </div>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <Field label={t('admin:groups.field.traffic_limit_gb')}>
          <NumberField {...common} unit="GB" value={form.traffic_limit_gb} onChange={v => setForm({ ...form, traffic_limit_gb: v })} />
        </Field>
        <Field label={t('admin:groups.field.ip_limit')}>
          <NumberField {...common} integer value={form.ip_limit} onChange={v => setForm({ ...form, ip_limit: v })} />
        </Field>
        {/* Stored on every panel, enforced on none: 3X-UI counts devices only at its own subscription endpoint, which
            PSP replaces. Warn wherever the number is typed, not only on the user form — a group value reaches more people. */}
        <Field label={t('admin:groups.field.device_limit')}
          hint={deviceCapIsInert(form.device_limit ?? 0)
            ? <span className="flex items-start gap-1 text-foreground"><IconWarning aria-hidden className={cn('mt-0.5 size-3.5 shrink-0', TONE_STYLE.warning.ink)} />{t('admin:users.field.device_limit_inert')}</span>
            : undefined}>
          <NumberField {...common} integer value={form.device_limit} onChange={v => setForm({ ...form, device_limit: v })} />
        </Field>
      </div>
    </div>
  )
}
