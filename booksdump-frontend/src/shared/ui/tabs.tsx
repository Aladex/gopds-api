import * as React from "react"
import { Tabs as TabsPrimitive } from "radix-ui"

import { cn } from "@/shared/lib/utils"

function Tabs({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Root>) {
  return (
    <TabsPrimitive.Root
      data-slot="tabs"
      // min-w-0 because a grid or flex item is min-width: auto by default,
      // which is its content's minimum: without this the tab list cannot
      // scroll, it widens the dialog holding it instead.
      className={cn("flex min-w-0 flex-col gap-3", className)}
      {...props}
    />
  )
}

/*
 * Two looks, one component.
 *
 * segmented is the app's tab bar: a rail with the chosen segment raised out of
 * it. underline is its secondary form, for tabs inside a tab — lighter, so the
 * two levels read as parent and child rather than as two rival bars. Both are
 * here rather than in the screens that use them, so that every screen gets
 * the same two and nobody grows a third.
 *
 * stacked lays a segmented bar out for a phone's sheet: equal columns, the
 * icon above its label. Which shell is showing is decided in JS (the sheet or
 * the dialog), so it is a prop rather than a media query that could disagree
 * with that decision by a pixel.
 */
type TabsVariant = "segmented" | "underline"

type TabsListContextValue = { variant: TabsVariant; stacked: boolean }

const TabsListContext = React.createContext<TabsListContextValue>({
  variant: "segmented",
  stacked: false,
})

// The list scrolls rather than wraps: how many tabs there are is not this
// component's decision, and a second row of them would push the panel below the
// fold on a phone. scrollbar-thin keeps the bar from drawing over the tabs.
function TabsList({
  className,
  variant = "segmented",
  stacked = false,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.List> & {
  variant?: TabsVariant
  stacked?: boolean
}) {
  const context = React.useMemo(
    () => ({ variant, stacked: stacked && variant === "segmented" }),
    [variant, stacked],
  )
  return (
    <TabsListContext.Provider value={context}>
      <TabsPrimitive.List
        data-slot="tabs-list"
        data-variant={variant}
        data-stacked={context.stacked || undefined}
        className={cn(
          // flex, not inline-flex: an inline box is sized by its content, so
          // the list would grow past its container rather than scroll inside
          // it.
          "scrollbar-thin flex w-full min-w-0 items-center justify-start overflow-x-auto",
          variant === "segmented" &&
            "gap-1 rounded-md bg-muted p-1 text-muted-foreground",
          variant === "underline" &&
            "gap-4 border-b border-border text-muted-foreground",
          className,
        )}
        {...props}
      />
    </TabsListContext.Provider>
  )
}

function TabsTrigger({
  className,
  icon: Icon,
  children,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Trigger> & {
  /** A lucide icon: beside the label, or above it in a stacked bar. */
  icon?: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>
}) {
  const { variant, stacked } = React.useContext(TabsListContext)
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      className={cn(
        // Labels never shorten: a tab whose name is cut is a tab nobody can
        // tell from its neighbour.
        "inline-flex items-center justify-center gap-1.5 text-sm font-medium whitespace-nowrap transition-colors",
        "outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50",
        "disabled:pointer-events-none disabled:opacity-50",
        "data-[state=active]:text-foreground",
        "[&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        variant === "segmented" && [
          "rounded-sm border border-transparent",
          // The chosen segment is a card-coloured surface with an outline.
          // On the light rail the fill alone is not enough — the page grey
          // and the rail sit too close to each other, and white on the rail
          // measures 2.6:1 — so the outline carries the 3:1 the shape needs:
          // foreground at half strength, laid over the rail, is 3.1:1 on the
          // light one and 4.2:1 on the dark. bg-clip-padding is what lays it
          // over the rail: by default the fill runs under the border too, and
          // the same half-black over white measured 1.5:1.
          "bg-clip-padding data-[state=active]:border-foreground/50 data-[state=active]:bg-card data-[state=active]:shadow-xs",
          // Every segment is as wide as its own label and the rail's spare
          // room is shared out equally (flex: 1 1 auto) — never cut, and never
          // narrower than its word: a bar with more than fits scrolls rather
          // than squeezing them. Equal thirds were tried for the profile's
          // sheet and «Подключения» needed 90px of a third's 81 at 320px.
          "flex-auto",
          stacked
            ? // The icon over the label, and a target at least 48px tall for
              // a thumb.
              "min-h-12 flex-col gap-0.5 px-1 py-1.5 text-[13px] [&_svg:not([class*='size-'])]:size-[18px]"
            : "h-9 px-3 sm:h-7 sm:text-[13px]",
        ],
        variant === "underline" && [
          // At least 44px to press, the line on the list's own rule.
          "-mb-px min-h-11 flex-none rounded-none border-b-2 border-transparent px-1",
          "data-[state=active]:border-foreground",
        ],
        className,
      )}
      {...props}
    >
      {Icon && <Icon aria-hidden />}
      {children}
    </TabsPrimitive.Trigger>
  )
}

function TabsContent({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return (
    <TabsPrimitive.Content
      data-slot="tabs-content"
      className={cn("outline-none", className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent }
