import * as React from "react"
import { cn } from "@/lib/utils"

/**
 * A native <select> in the style of Input; fast, accessible and fine for short lists.
 *
 * The chevron is the select's own background image (styles/ui.css, `[data-slot=native-select]`),
 * not a sibling icon in a wrapper. A wrapper was as wide as the row while a caller's `w-40` or
 * `max-w-*` (Field caps every select at 40rem) narrowed only the select, so the icon sat outside
 * the field's border. Painted by the select itself, it always lies inside its own box, whatever
 * width the select is given.
 */
function NativeSelect({ className, children, ...props }: React.ComponentProps<"select">) {
  return (
    <select
      data-slot="native-select"
      className={cn(
        "block h-(--control-h) w-full min-w-0 appearance-none rounded-lg border border-input bg-transparent py-1 pr-8 pl-2.5 text-base transition-colors outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:bg-muted disabled:opacity-50 md:text-sm dark:bg-foreground/[0.04] dark:[&>option]:bg-popover",
        className
      )}
      {...props}
    >
      {children}
    </select>
  )
}

export { NativeSelect }
