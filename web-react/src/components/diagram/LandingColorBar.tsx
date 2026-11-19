import { cn } from '@/lib/utils'
import { landingColorVar } from './landingColor'
import '@/styles/data-tokens.css'

/**
 * The thin coloured bar at the start of a line row (plan 3A): the same landing
 * node is the same colour wherever it appears. Purely decorative: the landing's
 * name is always in the row as text.
 */
export function LandingColorBar({ id, className }: { id: string | number; className?: string }) {
  return (
    <span
      aria-hidden="true"
      data-slot="landing-color-bar"
      className={cn('inline-block w-1 shrink-0 self-stretch rounded-full', className)}
      style={{ backgroundColor: landingColorVar(id) }}
    />
  )
}
