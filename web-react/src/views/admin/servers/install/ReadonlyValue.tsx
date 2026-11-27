import type { ReactNode } from 'react'
import { CodeField } from '@/components/CodeField'
import { CopyButton } from '@/components/common/CopyButton'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'

/** A value the page shows and the operator copies: the connection endpoint, the agent id. */
export function ReadonlyValue({ label, value, copy = true }: { label: ReactNode; value: string; copy?: boolean }) {
  return (
    <Field label={label}>
      {p => (
        <InputGroup>
          <InputGroupInput id={p.id} readOnly value={value} spellCheck={false} className="font-mono" />
          {copy && <InputGroupAddon align="inline-end"><CopyButton value={value} size="icon-xs" side="top" /></InputGroupAddon>}
        </InputGroup>
      )}
    </Field>
  )
}

/** A secret the page shows only on request: masked, revealed and copied by explicit buttons, never by default. */
export function ReadonlySecret({ label, value }: { label: ReactNode; value: string }) {
  return <Field label={label}><SecretField readOnly value={value} /></Field>
}

/** A command or file body to read and copy. `label` names the text for assistive technology. */
export function ReadonlyText({ label, value, rows = 3, hint }: { label: ReactNode; value: string; rows?: number; hint?: ReactNode }) {
  return (
    <CodeField label={label} hint={hint} readOnly wrap language="plain" minRows={rows} maxRows={Math.max(rows, 16)} value={value} onChange={() => {}} />
  )
}
