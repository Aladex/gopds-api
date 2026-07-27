import * as React from "react"
import { Progress as ProgressPrimitive } from "radix-ui"

import { cn } from "@/shared/lib/utils"

/**
 * Progress is a bar, determinate or not.
 *
 * The admin screens need both: a scan that reports a percentage, and one that
 * has only started. Passing no value gives the second — a stripe travelling the
 * width of the track, which says "working" without claiming to know how far
 * along it is. Radix models that as a null value, and reports it to assistive
 * technology as an indeterminate bar.
 */
function Progress({
  className,
  value,
  ...props
}: React.ComponentProps<typeof ProgressPrimitive.Root>) {
  const indeterminate = value === undefined || value === null

  return (
    <ProgressPrimitive.Root
      data-slot="progress"
      value={indeterminate ? null : value}
      className={cn(
        "relative h-1.5 w-full overflow-hidden rounded-full bg-primary/20",
        className
      )}
      {...props}
    >
      <ProgressPrimitive.Indicator
        data-slot="progress-indicator"
        className={cn(
          "h-full bg-primary transition-transform",
          indeterminate
            ? "w-1/3 animate-progress-slide motion-reduce:w-full motion-reduce:animate-none motion-reduce:opacity-60"
            : "w-full"
        )}
        style={indeterminate ? undefined : { transform: `translateX(-${100 - (value ?? 0)}%)` }}
      />
    </ProgressPrimitive.Root>
  )
}

export { Progress }
