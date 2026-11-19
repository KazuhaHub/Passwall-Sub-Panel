import { Autocomplete as AutocompletePrimitive } from "@base-ui/react/autocomplete"
import { cn } from "@/lib/utils"

import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { ChevronDownIcon } from "lucide-react"

/** Free text with a dropdown of suggestions that narrows as you type (the value is the text itself). */
const Autocomplete = AutocompletePrimitive.Root

function AutocompleteInput({
  className,
  disabled = false,
  ...props
}: AutocompletePrimitive.Input.Props) {
  return (
    <InputGroup className={cn("w-auto", className)}>
      <AutocompletePrimitive.Input render={<InputGroupInput disabled={disabled} />} {...props} />
      <InputGroupAddon align="inline-end">
        <InputGroupButton
          size="icon-xs"
          variant="ghost"
          render={<AutocompletePrimitive.Trigger />}
          data-slot="input-group-button"
          className="data-pressed:bg-transparent"
          disabled={disabled}
          aria-label="open"
        >
          <ChevronDownIcon className="pointer-events-none size-4 text-muted-foreground" />
        </InputGroupButton>
      </InputGroupAddon>
    </InputGroup>
  )
}

function AutocompleteContent({ className, ...props }: AutocompletePrimitive.Popup.Props) {
  return (
    <AutocompletePrimitive.Portal>
      <AutocompletePrimitive.Positioner side="bottom" sideOffset={6} align="start" className="isolate z-50">
        <AutocompletePrimitive.Popup
          data-slot="autocomplete-content"
          className={cn(
            "group/autocomplete-content relative max-h-(--available-height) w-(--anchor-width) max-w-(--available-width) origin-(--transform-origin) overflow-hidden rounded-lg bg-popover text-popover-foreground shadow-md ring-1 ring-foreground/10 duration-100 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
            className
          )}
          {...props}
        />
      </AutocompletePrimitive.Positioner>
    </AutocompletePrimitive.Portal>
  )
}

function AutocompleteList({ className, ...props }: AutocompletePrimitive.List.Props) {
  return (
    <AutocompletePrimitive.List
      data-slot="autocomplete-list"
      className={cn(
        "no-scrollbar max-h-[min(18rem,calc(var(--available-height)-1rem))] scroll-py-1 overflow-y-auto overscroll-contain p-1 data-empty:p-0",
        className
      )}
      {...props}
    />
  )
}

function AutocompleteItem({ className, ...props }: AutocompletePrimitive.Item.Props) {
  return (
    <AutocompletePrimitive.Item
      data-slot="autocomplete-item"
      className={cn(
        "relative flex w-full cursor-default items-center gap-2 rounded-md px-1.5 py-1 text-sm outline-hidden select-none data-highlighted:bg-accent data-highlighted:text-accent-foreground",
        className
      )}
      {...props}
    />
  )
}

export { Autocomplete, AutocompleteInput, AutocompleteContent, AutocompleteList, AutocompleteItem }
