import * as React from "react"
import { Tooltip as TooltipPrimitive } from "radix-ui"

import { cn } from "@/shared/lib/utils"

/*
 * The overlays render at z-modal, not shadcn's z-50: this application pins a
 * header and a bottom bar over the page at 1100-1200, and a menu opening at 50
 * disappears behind them.
 */

function TooltipProvider({
  delayDuration = 0,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return (
    <TooltipPrimitive.Provider
      data-slot="tooltip-provider"
      delayDuration={delayDuration}
      {...props}
    />
  )
}

/*
 * A tooltip opens on hover and on keyboard focus — and on no other focus.
 *
 * Radix opens it on every focus that does not follow a press on the trigger.
 * A dialog that closes gives the focus back to the button that opened it,
 * and that focus is the script's, not the reader's: the hint opened on it and
 * stayed over the card after the dialog had gone, until something else was
 * pressed. So a focus opens the hint only when a Tab put it there. The focus
 * itself still goes back to the trigger.
 */
let focusByTab = false
if (typeof document !== "undefined") {
  document.addEventListener(
    "keydown",
    (event) => {
      focusByTab = event.key === "Tab"
    },
    true,
  )
  document.addEventListener(
    "pointerdown",
    () => {
      focusByTab = false
    },
    true,
  )
}

type TooltipFocusContextValue = { holdFocusOpen: () => void }

const TooltipFocusContext =
  React.createContext<TooltipFocusContextValue | null>(null)

function Tooltip({
  open: openProp,
  defaultOpen = false,
  onOpenChange,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Root>) {
  const [uncontrolledOpen, setUncontrolledOpen] = React.useState(defaultOpen)
  const open = openProp ?? uncontrolledOpen
  // Set by the trigger for the length of one focus event: Radix asks to open
  // from inside it, synchronously.
  const focusHeld = React.useRef(false)
  const setOpen = React.useCallback(
    (next: boolean) => {
      if (next && focusHeld.current) return
      if (openProp === undefined) setUncontrolledOpen(next)
      onOpenChange?.(next)
    },
    [openProp, onOpenChange],
  )
  const context = React.useMemo(
    () => ({
      holdFocusOpen: () => {
        focusHeld.current = true
        queueMicrotask(() => {
          focusHeld.current = false
        })
      },
    }),
    [],
  )
  return (
    <TooltipFocusContext.Provider value={context}>
      <TooltipPrimitive.Root
        data-slot="tooltip"
        open={open}
        onOpenChange={setOpen}
        {...props}
      />
    </TooltipFocusContext.Provider>
  )
}

function TooltipTrigger({
  onFocus,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Trigger>) {
  const tooltip = React.useContext(TooltipFocusContext)
  return (
    <TooltipPrimitive.Trigger
      data-slot="tooltip-trigger"
      onFocus={(event) => {
        onFocus?.(event)
        // Runs before Radix's own focus handler, which opens the hint.
        if (!focusByTab) tooltip?.holdFocusOpen()
      }}
      {...props}
    />
  )
}

function TooltipContent({
  className,
  sideOffset = 0,
  children,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        sideOffset={sideOffset}
        className={cn(
          "z-modal w-fit origin-(--radix-tooltip-content-transform-origin) animate-in rounded-md bg-foreground px-3 py-1.5 text-xs text-balance text-background fade-in-0 zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
          className,
        )}
        {...props}
      >
        {children}
        <TooltipPrimitive.Arrow className="z-modal size-2.5 translate-y-[calc(-50%_-_2px)] rotate-45 rounded-[2px] bg-foreground fill-foreground" />
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  )
}

export { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider }
