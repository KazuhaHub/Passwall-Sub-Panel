import type { ComponentProps, ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { TOUCH_TARGET } from './touchTarget'

type TooltipSide = 'top' | 'right' | 'bottom' | 'left'

/**
 * The only way to draw an icon-only button: it always has an accessible name AND
 * the same words as a tooltip, so a pointer user, a keyboard user and a screen
 * reader all get the label (plan 3.5). `label` is required for that reason.
 *
 * It forwards every other prop (and the ref) to the button, so it can be the
 * `render` of a menu or popover trigger: `<DropdownMenuTrigger render={<IconButton
 * label="更多" />} />` gets both the menu behaviour and the tooltip.
 *
 * Switch a control off with `aria-disabled`, not `disabled`: a disabled button cannot
 * be focused and shows no tooltip, so nobody can learn the reason. The click is
 * ignored here, the look follows the attribute, and `tooltip` can then say why.
 */
export function IconButton({ label, tooltip, side = 'bottom', variant = 'ghost', size = 'icon', onClick, className, ...props }: Omit<ComponentProps<typeof Button>, 'aria-label'> & {
  label: string
  /** When the tooltip should say more than the accessible name (or why the control is off). */
  tooltip?: ReactNode
  side?: TooltipSide
}) {
  const off = props['aria-disabled'] === true || props['aria-disabled'] === 'true'
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            aria-label={label}
            variant={variant}
            size={size}
            {...props}
            className={cn(TOUCH_TARGET, 'aria-disabled:cursor-not-allowed aria-disabled:opacity-50', className)}
            onClick={off ? undefined : onClick}
          />
        }
      />
      <TooltipContent side={side}>{tooltip ?? label}</TooltipContent>
    </Tooltip>
  )
}
