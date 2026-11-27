import { useId, useRef, useState } from 'react'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { IconAdd, IconClose } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { useNodesT } from '../shared/useNodesT'
import { MONO } from './controls'

// crypto/tls.CipherSuites(), in the same ID-sorted order as Go's official
// source. InsecureCipherSuites() is intentionally excluded: Go documents those
// separately because their primitives or design have known security issues.
export const TLS_CIPHER_SUITES = [
  'TLS_AES_128_GCM_SHA256',
  'TLS_AES_256_GCM_SHA384',
  'TLS_CHACHA20_POLY1305_SHA256',
  'TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA',
  'TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA',
  'TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA',
  'TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA',
  'TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256',
  'TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384',
  'TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256',
  'TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384',
  'TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256',
  'TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256',
] as const

const split = (value: string) => (value ? value.split(':').map(s => s.trim()).filter(Boolean) : [])

/**
 * Xray takes the suites as ONE colon-delimited string, while the person picks from a fixed list, so the
 * field shows that string as a read-only preview and opens the list when it is focused. Every suite stays
 * in the list after it is picked and its button turns from add to remove, so one row adds or removes
 * without the list closing. The preview is generated, not typed into: Backspace and Delete do nothing, and
 * every change comes from a suite's own button.
 */
export function CipherSuitesField({ label, hint, value, onChange }: {
  label: string
  hint: string
  value: string
  onChange: (next: string) => void
}) {
  const t = useNodesT()
  const listId = useId()
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const selected = split(value)

  const toggle = (suite: string) => onChange((selected.includes(suite) ? selected.filter(s => s !== suite) : [...selected, suite]).join(':'))
  // The suite name is appended rather than interpolated: it is a protocol identifier that never translates.
  const action = (suite: string, on: boolean) => `${on ? t('nodes.create_dialog.tls_cipher_remove') : t('nodes.create_dialog.tls_cipher_add')} ${suite}`

  return (
    <Field label={label} hint={hint}>
      {({ id, 'aria-describedby': describedBy }) => (
        <div ref={box}
          onBlur={e => { if (!box.current?.contains(e.relatedTarget as Node | null)) setOpen(false) }}
          onKeyDown={e => { if (e.key === 'Escape' && open) { e.stopPropagation(); setOpen(false) } }}
          className="flex flex-col gap-2">
          <Input id={id} role="combobox" aria-expanded={open} aria-controls={listId} aria-describedby={describedBy}
            readOnly aria-readonly title={selected.join(':')} value={selected.join(':')} className={cn(MONO, 'cursor-pointer')}
            onFocus={() => setOpen(true)} onClick={() => setOpen(true)} />
          {open && (
            // Keeping focus on the preview while a suite is pressed: some browsers do not focus a clicked button, and the list would close first.
            <ul id={listId} onMouseDown={e => e.preventDefault()} className="flex flex-wrap gap-2 rounded-lg border bg-popover p-2">
              {TLS_CIPHER_SUITES.map(suite => {
                const on = selected.includes(suite)
                return (
                  <li key={suite}>
                    <button type="button" aria-label={action(suite, on)} aria-pressed={on} onClick={() => toggle(suite)}
                      className={cn('flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs outline-none focus-visible:ring-3 focus-visible:ring-ring/50', MONO,
                        on ? 'border-primary bg-primary/10' : 'border-border hover:bg-muted')}>
                      {suite}
                      {on ? <IconClose className="size-3.5" aria-hidden /> : <IconAdd className="size-3.5" aria-hidden />}
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      )}
    </Field>
  )
}
