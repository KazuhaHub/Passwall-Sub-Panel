import type { ComponentProps, ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

/**
 * An icon-only button that always has both a tooltip and an aria-label: the
 * rule for every icon-only control (plan 3.5). `label` is the one string that
 * feeds both, so the two can never drift apart.
 */
export function TipButton({
  label, children, variant = 'ghost', size = 'icon-sm', ...props
}: Omit<ComponentProps<typeof Button>, 'aria-label' | 'children'> & { label: string; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger render={<Button variant={variant} size={size} aria-label={label} {...props} />}>
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}
