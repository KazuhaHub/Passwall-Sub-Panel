import { cn } from '@/lib/utils'
import { landingColorStyle, type LandingColor } from './landingColor'
import '@/styles/data-tokens.css'

/**
 * The thin coloured bar at the start of a line row (plan 3A): the same landing
 * node is the same colour wherever it appears on the page (colours come from
 * assignLandingColors over the page's landing list). Purely decorative: the
 * landing's name is always in the row as text, and a reused colour is striped.
 */
export function LandingColorBar({ color, className }: { color: LandingColor | undefined; className?: string }) {
  if (!color) return null
  return (
    <span
      aria-hidden="true"
      data-slot="landing-color-bar"
      className={cn('inline-block w-1 shrink-0 self-stretch rounded-full', className)}
      style={landingColorStyle(color)}
    />
  )
}
