import { memo } from 'react'
import LegalTab from '../legal/LegalTab'
import { SUB_KEYS } from './model'
import { SectionForm } from './parts'
import { SubTabs, type SubPanel } from './SubTabs'
import { useUiSection } from './useUiSection'

const noProblems = () => ({})

/**
 * Privacy and terms. The two switches belong to the settings record and ride this section's Save; the documents are
 * written by the publish dialog alone (LegalTab), which is why the section's form holds only the switches.
 */
function LegalPanel({ onDirtyChange }: { onDirtyChange: (dirty: boolean) => void }) {
  const form = useUiSection(SUB_KEYS.legal.main, onDirtyChange, noProblems)
  const { value: v, patch } = form
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard} plain>
      <LegalTab enabled={v.legal_enabled ?? false} onEnabledChange={c => patch({ legal_enabled: c })}
        discloseUsage={v.legal_disclose_usage ?? false} onDiscloseUsageChange={c => patch({ legal_disclose_usage: c })} />
    </SectionForm>
  )
}

const PANELS = { main: LegalPanel } satisfies Record<string, SubPanel>

/** One screen, so no sub-tab bar. */
export const LegalSection = memo(function LegalSection({ active }: { active: boolean }) {
  return <SubTabs section="legal" active={active} panels={PANELS} />
})
