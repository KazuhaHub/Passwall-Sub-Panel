import { FormSection } from '@/components/form/FormSection'
import { useNodesT } from '../shared/useNodesT'
import { AreaField, type SectionProps } from './controls'

/**
 * JSON mode: the three raw documents replace the structured per-protocol fields. Saving sends these strings
 * to 3X-UI as they are, so any option the form does not model (mKCP, h2, splithttp...) can still be set.
 * The field names are 3X-UI's own and are not translated.
 */
export function RawJsonEditor({ form, update }: Pick<SectionProps, 'form' | 'update'>) {
  const t = useNodesT()
  return (
    <FormSection title={t('nodes.create_dialog.section_advanced')} description={t('nodes.create_dialog.advanced_hint')}>
      <AreaField label="settings" rows={6} value={form.raw_settings ?? ''} onChange={v => update('raw_settings', v)} />
      <AreaField label="streamSettings" rows={6} value={form.raw_stream_settings ?? ''} onChange={v => update('raw_stream_settings', v)} />
      <AreaField label="sniffing" rows={4} value={form.raw_sniffing ?? ''} onChange={v => update('raw_sniffing', v)} />
    </FormSection>
  )
}
