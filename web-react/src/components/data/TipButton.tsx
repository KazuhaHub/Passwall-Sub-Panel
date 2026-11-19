import type { ComponentProps, ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'
import { TOUCH_TARGET } from './touchTarget'

/**
 * An icon-only button that always has both a tooltip and an aria-label: the
 * rule for every icon-only control (plan 3.5). `label` is the one string that
 * feeds both, so the two can never drift apart. `tooltip` overrides only the
 * tooltip text, for a control that is switched off and says why.
 *
 * Switch a control off with `aria-disabled`, not `disabled`: a disabled button
 * cannot be focused and shows no tooltip, so nobody can learn the reason. The
 * click is ignored here, and the look follows the attribute.
 */
export function TipButton({
  label, tooltip, children, variant = 'ghost', size = 'icon-sm', className, onClick, ...props
}: Omit<ComponentProps<typeof Button>, 'aria-label' | 'children'> & { label: string; tooltip?: ReactNode; children: ReactNode }) {
  const off = props['aria-disabled'] === true || props['aria-disabled'] === 'true'
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant={variant}
            size={size}
            aria-label={label}
            className={cn(TOUCH_TARGET, 'aria-disabled:cursor-not-allowed aria-disabled:opacity-50', className)}
            onClick={off ? undefined : onClick}
            {...props}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{tooltip ?? label}</TooltipContent>
    </Tooltip>
  )
}
