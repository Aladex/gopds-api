import * as React from "react"

import { cn } from "@/shared/lib/utils"

/**
 * Field is a labelled control.
 *
 * The label is a real <label> rather than a placeholder so it survives the
 * field being filled in — a form of eight inputs is unreadable once the hints
 * that named them have been typed over. It is also what lets a screen reader,
 * and a test, find the control by the name a person would use for it.
 */
function Field({
  id,
  label,
  children,
  hint,
  error,
  className,
}: {
  id: string
  label: React.ReactNode
  children: React.ReactNode
  hint?: string
  error?: string
  className?: string
}) {
  return (
    <div className={cn("flex flex-col gap-1", className)}>
      <label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </label>
      {children}
      {error ? (
        <p id={`${id}-hint`} role="alert" className="text-xs text-destructive">
          {error}
        </p>
      ) : (
        hint && (
          <p id={`${id}-hint`} className="text-xs text-muted-foreground">
            {hint}
          </p>
        )
      )}
    </div>
  )
}

export { Field }
