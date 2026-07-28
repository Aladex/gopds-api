import * as React from "react"

import { cn } from "@/shared/lib/utils"

/**
 * inputFrame is everything that makes a field look like a field: its height,
 * its border, its background.
 *
 * It is exported so a control that has to put something else inside the same
 * frame — a chip naming what the field is scoped to, say — can wear the frame
 * on a wrapper and let a bare input fill the rest. Copying these classes to the
 * call site would work until the day one of them changes here and not there.
 */
const inputFrame =
  "h-10 w-full min-w-0 sm:h-8 rounded-md border border-input bg-transparent px-3 py-1 text-base shadow-xs transition-[color,box-shadow] outline-none selection:bg-primary selection:text-primary-foreground placeholder:text-muted-foreground disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-[13px] dark:bg-input/30"

/** The ring is drawn on whatever element owns the frame. */
const inputFocusRing =
  "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"

const inputInvalid =
  "aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40"

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        inputFrame,
        "file:inline-flex file:h-7 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground",
        inputFocusRing,
        inputInvalid,
        className,
      )}
      {...props}
    />
  )
}

export { Input, inputFrame, inputFocusRing, inputInvalid }
