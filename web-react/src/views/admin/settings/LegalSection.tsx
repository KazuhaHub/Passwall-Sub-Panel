import { memo } from 'react'
import LegalTab from '../legal/LegalTab'
import { SECTION_KEYS, type KeysOf } from './model'
import { SectionForm } from './parts'
import { useUiSection } from './useUiSection'

const keys = SECTION_KEYS.legal
const noProblems = () => ({})

/**
 * Privacy and terms. The two switches belong to the settings record and ride this section's Save; the documents are
 * written by the publish dialog alone (LegalTab), which is why the section's form holds only the switches.
 */
export const LegalSection = memo(function LegalSection({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const form = useUiSection<KeysOf<'legal'>>(keys, onDirtyChange, noProblems)
  const { value: v, patch } = form
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard} plain>
      <LegalTab enabled={v.legal_enabled ?? false} onEnabledChange={c => patch({ legal_enabled: c })}
        discloseUsage={v.legal_disclose_usage ?? false} onDiscloseUsageChange={c => patch({ legal_disclose_usage: c })} />
    </SectionForm>
  )
})
