import * as React from "react"

import { cn } from "@/shared/lib/utils"

/**
 * BouncingDots says something is on its way.
 *
 * Three dots hopping in turn rather than a spinner: a spinner at this size is a
 * ring of a few pixels that reads as a smudge, and it turns at the same rate
 * whatever it is waiting for. The dots stay legible small, and the stagger is
 * the whole of the effect — without it the three move as one and it looks like
 * a stutter.
 *
 * Decoration, not information: whatever is loading names itself elsewhere, so
 * this is hidden from assistive technology. A reader who asked for less motion
 * gets the three dots standing still.
 */
const DELAYS = ['0ms', '160ms', '320ms']

function BouncingDots({
  className,
  dotClassName,
  ...props
}: React.ComponentProps<'span'> & { dotClassName?: string }) {
  return (
    <span
      aria-hidden="true"
      data-slot="bouncing-dots"
      className={cn('inline-flex items-end gap-1', className)}
      {...props}
    >
      {DELAYS.map((delay) => (
        <span
          key={delay}
          style={{ animationDelay: delay }}
          className={cn(
            'size-1.5 rounded-full bg-current animate-dot-hop',
            'motion-reduce:animate-none motion-reduce:opacity-60',
            dotClassName,
          )}
        />
      ))}
    </span>
  )
}

export { BouncingDots }
