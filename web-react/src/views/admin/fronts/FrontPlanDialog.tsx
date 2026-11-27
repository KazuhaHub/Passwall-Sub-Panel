import { useRef } from 'react'
import { frontErrorWording, type FrontError } from '@/api/fronts'
import { Button } from '@/components/ui/button'
import { ImpactDialogFrame } from '../nodes/delete/ImpactDialogFrame'
import { useAdminT } from '../useAdminT'

// The lists a refusal can carry, in the order they are read, with the sentence that introduces each.
const LISTS = [
  ['leaving_listeners', 'admin:servers.fronts.plan.listeners'],
  ['leaving_lines', 'admin:servers.fronts.plan.lines'],
  ['tunnels', 'admin:servers.fronts.plan.tunnels'],
] as const

const asList = (v: unknown): string[] => (Array.isArray(v) ? v : v === undefined || v === null || v === '' ? [] : [v]).map(String)

/**
 * What the server says deleting a shared port would take with it (409 confirm_required), laid out as its own
 * dialog the way the node pages lay out their plans: the server's sentence first, then each affected thing as a
 * list, and a button that names the verb. Focus opens on Cancel. Nothing is sent until the button is pressed.
 */
export default function FrontPlanDialog({ error, title, confirmLabel, onAnswer }: {
  error: FrontError
  title: string
  confirmLabel: string
  onAnswer: (ok: boolean) => void
}) {
  const t = useAdminT()
  const cancelRef = useRef<HTMLButtonElement>(null)
  const wording = frontErrorWording(error)
  const lists = LISTS.map(([key, label]) => ({ label, items: asList(error.params[key]) })).filter(l => l.items.length > 0)
  return (
    <ImpactDialogFrame
      title={title} cancelRef={cancelRef} onClose={() => onAnswer(false)}
      actions={<Button variant="destructive" onClick={() => onAnswer(true)}>{confirmLabel}</Button>}
    >
      <p data-testid="front-plan-text">{t(wording.key, wording.values) as string}</p>
      {lists.map(l => (
        <div key={l.label}>
          <p className="text-muted-foreground">{t(l.label)}</p>
          <ul className="list-disc ps-5">{l.items.map(i => <li key={i}>{i}</li>)}</ul>
        </div>
      ))}
    </ImpactDialogFrame>
  )
}
