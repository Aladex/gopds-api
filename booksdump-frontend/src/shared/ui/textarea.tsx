import * as React from "react"

import { cn } from "@/shared/lib/utils"

/**
 * Textarea carries the same frame as Input.
 *
 * Without preflight a bare textarea keeps the browser's own box, so everything
 * the frame is made of is spelled out here rather than inherited — which is
 * also why this exists as a component at all instead of a class or two at each
 * call site.
 */
function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "w-full min-w-0 rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-xs outline-none transition-[color,box-shadow] md:text-sm dark:bg-input/30",
        "field-sizing-content min-h-16 resize-y",
        "placeholder:text-muted-foreground",
        "focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50",
        "disabled:cursor-not-allowed disabled:opacity-50",
        "aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40",
        className,
      )}
      {...props}
    />
  )
}

export { Textarea }
