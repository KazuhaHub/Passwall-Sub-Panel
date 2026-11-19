import type { ComponentProps } from 'react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

// A phone finger needs a 32px target at the least. The icon buttons are 28px (compact
// `icon-sm`) or 24px (`icon-xs`), which is right for a mouse, so on a coarse pointer the
// BUTTON ITSELF grows to 2rem; on a mouse nothing changes. It grows the real box, not an
// invisible hit area around it: an expanded hit area of an `icon-xs` button takes 4px of a
// neighbour's visible area unless a gap is enforced everywhere, whereas a larger box just
// lays out larger and can never overlap. Exported so a trigger that is not an IconButton
// (InfoTip) carries the same rule. scripts/dist-guards.mjs checks the built CSS has it.
export const TOUCH_TARGET = 'pointer-coarse:min-h-[2rem] pointer-coarse:min-w-[2rem]'

type TooltipSide = 'top' | 'right' | 'bottom' | 'left'

/**
 * The only way to draw an icon-only button: it always has an accessible name AND
 * the same words as a tooltip, so a pointer user, a keyboard user and a screen
 * reader all get the label (plan 3.5). `label` is required for that reason.
 *
 * It forwards every other prop (and the ref) to the button, so it can be the
 * `render` of a menu or popover trigger: `<DropdownMenuTrigger render={<IconButton
 * label="更多" />} />` gets both the menu behaviour and the tooltip.
 */
export function IconButton({ label, tooltip, side = 'bottom', variant = 'ghost', size = 'icon', ...props }: Omit<ComponentProps<typeof Button>, 'aria-label'> & {
  label: string
  /** When the tooltip should say more than the accessible name. */
  tooltip?: string
  side?: TooltipSide
}) {
  return (
    <Tooltip>
      <TooltipTrigger render={<Button aria-label={label} variant={variant} size={size} {...props} className={cn(TOUCH_TARGET, props.className)} />} />
      <TooltipContent side={side}>{tooltip ?? label}</TooltipContent>
    </Tooltip>
  )
}
