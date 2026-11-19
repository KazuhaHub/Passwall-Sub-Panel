import { Tabs as TabsPrimitive } from "@base-ui/react/tabs"
import { useContext, useRef } from "react"
import { cn } from "@/lib/utils"
import { InFormContext, useRevealOnProblem } from "@/lib/reveal"

function Tabs({ className, ...props }: TabsPrimitive.Root.Props) {
  return <TabsPrimitive.Root data-slot="tabs" className={cn("flex flex-col gap-4", className)} {...props} />
}

function TabsList({ className, ...props }: TabsPrimitive.List.Props) {
  return (
    <TabsPrimitive.List
      data-slot="tabs-list"
      className={cn("relative inline-flex h-[calc(var(--control-h)+0.25rem)] w-fit items-center gap-1 rounded-lg bg-muted p-[3px] text-muted-foreground", className)}
      {...props}
    />
  )
}

function TabsTrigger({ className, ...props }: TabsPrimitive.Tab.Props) {
  return (
    <TabsPrimitive.Tab
      data-slot="tabs-trigger"
      className={cn(
        "relative z-10 inline-flex h-[calc(100%-1px)] items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium whitespace-nowrap transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 data-active:text-foreground [&_svg]:size-4",
        className
      )}
      {...props}
    />
  )
}

function TabsIndicator({ className, ...props }: TabsPrimitive.Indicator.Props) {
  return (
    <TabsPrimitive.Indicator
      data-slot="tabs-indicator"
      className={cn(
        "absolute top-1/2 left-0 z-0 h-[calc(100%-6px)] w-(--active-tab-width) -translate-y-1/2 translate-x-(--active-tab-left) rounded-md bg-background shadow-sm ring-1 ring-foreground/5 transition-[translate,width] duration-200 ease-out",
        className
      )}
      {...props}
    />
  )
}

/**
 * Inside a FormValidityProvider an inactive panel stays mounted (hidden) by default, so the fields
 * in it keep reporting an unusable value: an unmounted field reports nothing, and a form would save
 * a value that nobody can see is wrong. Outside a form the default is unchanged. An explicit
 * `keepMounted={false}` is honoured, and then fields in a hidden tab cannot block Save.
 */
function TabsContent({ className, keepMounted, ...props }: TabsPrimitive.Panel.Props) {
  const inForm = useContext(InFormContext)
  // A panel kept mounted while inactive still holds fields that can have problems. Registering
  // it lets "go to the first problem" switch to its tab: the panel names its tab through
  // aria-labelledby, and activating that tab works the same whether the tabs are controlled or not.
  const ref = useRef<HTMLDivElement>(null)
  useRevealOnProblem(ref, () => {
    const tab = document.getElementById(ref.current?.getAttribute("aria-labelledby") ?? "")
    tab?.click()
  })
  return <TabsPrimitive.Panel ref={ref} keepMounted={keepMounted ?? inForm} data-slot="tabs-content" className={cn("outline-none", className)} {...props} />
}

export { Tabs, TabsList, TabsTrigger, TabsIndicator, TabsContent }
