import * as React from "react"

import { cn } from "@/shared/lib/utils"

/**
 * Expandable animates between a fixed-height peek and the full content.
 *
 * It animates only what the reader changes. Arriving already open, or already
 * collapsed, is not a transition — the collapsed height is CSS, in the
 * element's own line heights, so the first paint is correct without measuring
 * anything.
 *
 * The obvious approaches do not animate at all: `display` and
 * `-webkit-line-clamp` are not interpolatable properties, and `height: auto`
 * has no numeric target to transition towards. So the height is measured and
 * driven explicitly:
 *
 *   opening — from the peek height to the measured scrollHeight, then released
 *             to `none` once the transition ends, so later reflows (a font
 *             loading, the window narrowing) are not clipped;
 *   closing — pinned back to the measured height first and read back to force
 *             the layout, then animated down to the peek height. Both halves
 *             matter: without the pin, and without making the browser take it
 *             before the peek height arrives, the panel jumps straight from
 *             `none` to the peek height in a single frame.
 *
 * Readers who asked for less motion get the same states without the transition.
 *
 * With `measuredPeek` the two heights are the content's own. The content then
 * clamps itself — line-clamp, truncate, a row of fixed height — through the
 * `clamped` flag its render prop receives, and keeps the ellipsis those clamps
 * draw. Such a peek cannot be written in CSS (one chip row plus a line of
 * colophon plus three lines of annotation is no number of this element's line
 * heights), so it is measured:
 *
 *   opening — the height on screen is pinned as the transition's start just
 *             as the clamps come off, and the height they were hiding becomes
 *             the target;
 *   closing — the full height is pinned first, then the clamps come back for
 *             one commit, before the browser has painted anything, to measure
 *             what the clamped content is now. That measurement, not one
 *             remembered from the opening, is the target: the card may have
 *             been resized or re-wrapped while it was open, and closing onto a
 *             stale height would land beside the content and jump when the
 *             clamps return. A close whose two heights turn out equal settles
 *             at once — a transition between equal heights never ends, and
 *             waiting for it would leave the panel stuck in its own closing
 *             phase.
 *
 * The clamps go back on for good only once the closing transition has landed,
 * because max-height can clip but never hold anything open — re-clamping
 * sooner would drop the content to its final height in a single frame. The
 * first paint is still unmeasured: the clamped content lays out at exactly the
 * peek height, so there is nothing to correct after the browser has painted.
 */

type Phase = "collapsed" | "opening" | "open" | "measuring" | "closing"

// The base is Omit'd of children: a render prop is not a valid ReactNode, and
// this component accepts either (react-i18next widens the inherited type).
export interface ExpandableProps extends Omit<
  React.HTMLAttributes<HTMLDivElement>,
  "children"
> {
  open: boolean
  /** How many lines of the content stay visible while collapsed. */
  peekLines?: number
  /** Fade the bottom edge of the peek instead of cutting text mid-line. */
  fade?: boolean
  /**
   * Measure both ends of the animation from the content, which clamps itself
   * (line-clamp, truncate, a fixed row) through the `clamped` flag its render
   * prop receives — instead of this element clipping N of its own line
   * heights. The fade does not apply: the content's own clamps already mark
   * the cut.
   */
  measuredPeek?: boolean
  children: React.ReactNode | ((clamped: boolean) => React.ReactNode)
}

function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  )
}

export const Expandable = React.forwardRef<HTMLDivElement, ExpandableProps>(
  (
    {
      open,
      peekLines = 2,
      fade = true,
      measuredPeek = false,
      className,
      children,
      ...rest
    },
    forwardedRef,
  ) => {
    const innerRef = React.useRef<HTMLDivElement | null>(null)

    /**
     * The collapsed height, in the element's own line heights.
     *
     * Expressed in CSS rather than measured, so it is already right on the
     * first paint. Measuring means rendering once unclamped and correcting
     * afterwards, which is a frame of full-height content followed by a
     * collapse the reader never asked for.
     */
    const collapsedHeight = `${peekLines}lh`

    const [phase, setPhase] = React.useState<Phase>(open ? "open" : "collapsed")
    const [maxHeight, setMaxHeight] = React.useState<string | undefined>(
      open || measuredPeek ? undefined : collapsedHeight,
    )

    /**
     * The full height a close started from, carried from the effect that
     * pinned it to the effect that measures the clamped target — two commits
     * apart, in the same flush, before the browser has painted either.
     */
    const closingFrom = React.useRef(0)

    const setRefs = React.useCallback(
      (node: HTMLDivElement | null) => {
        innerRef.current = node
        if (typeof forwardedRef === "function") {
          forwardedRef(node)
        } else if (forwardedRef) {
          ;(
            forwardedRef as React.MutableRefObject<HTMLDivElement | null>
          ).current = node
        }
      },
      [forwardedRef],
    )

    /**
     * Whether `open` has actually changed since mounting.
     *
     * A panel arrives in the state it was asked for, and animating into it
     * would mean showing the reader the other one first — every card in a
     * freshly loaded list unfolding and folding itself back up.
     */
    const settled = React.useRef(false)

    /**
     * The whole measured handoff runs in layout effects, in the interaction's
     * own task.
     *
     * A commit's layout effects run before that commit can be painted; its
     * passive effects do not. The measuring commit puts the clamps back on,
     * so the restore that follows it may not be passive — the browser could
     * paint the content collapsed for a frame before the close starts moving.
     * Here the measurement and the restore happen in the same pre-paint pass,
     * and the re-render they cause is flushed before the browser gets a frame.
     */
    React.useLayoutEffect(() => {
      const node = innerRef.current
      if (!node) return

      if (!settled.current) {
        settled.current = true
        return
      }

      if (prefersReducedMotion()) {
        node.style.maxHeight = ""
        setPhase(open ? "open" : "collapsed")
        setMaxHeight(measuredPeek || open ? undefined : collapsedHeight)
        return
      }

      if (measuredPeek) {
        if (open) {
          // The clamped content is still on screen here, so what it measures
          // is the transition's start. The pin is written to the node and
          // read back so the browser computes it before the target arrives in
          // the next commit.
          node.style.maxHeight = `${node.clientHeight}px`
          void node.offsetHeight
          // The re-render this causes takes the clamps off; the effect below
          // then measures what they were hiding and sets the target.
          setPhase("opening")
          return
        }
        // Pin the full height so there is something to animate down from,
        // then hand the measurement of the clamped target to the effect below,
        // which runs one commit later — with the clamps back on, pre-paint.
        closingFrom.current = node.scrollHeight
        node.style.maxHeight = `${closingFrom.current}px`
        void node.offsetHeight
        setPhase("measuring")
        return
      }

      if (open) {
        setPhase("opening")
        setMaxHeight(`${node.scrollHeight}px`)
        return
      }

      // Pin the current height so there is something to animate down
      // from. The pin is written to the node and read back, rather than
      // set as state: React would commit it and the peek height in the
      // same frame, the browser would only ever compute the second, and
      // the panel would snap shut with no transition at all.
      node.style.maxHeight = `${node.scrollHeight}px`
      void node.offsetHeight

      setPhase("closing")
      setMaxHeight(collapsedHeight)
    }, [open, collapsedHeight, measuredPeek])

    /**
     * The handoff through the measuring commit — a layout effect, like the one
     * above it, so that the restore of the unclamped content is part of the
     * same pre-paint pass as the commit that clamped it. One deferred effect
     * anywhere in the chain leaves a frame in which the browser can paint the
     * content collapsed before the close has begun to move.
     */
    React.useLayoutEffect(() => {
      if (!measuredPeek) return
      const node = innerRef.current
      if (!node) return

      if (phase === "opening") {
        const full = node.scrollHeight
        if (Math.abs(full - node.clientHeight) <= 1) {
          // Nothing was held back after all, and a transition between two
          // equal heights never ends — waiting for it would leave the panel
          // stuck in its own opening phase. The pin is this element's own
          // write, so it is this element's job to take back.
          node.style.maxHeight = ""
          setPhase("open")
          setMaxHeight(undefined)
          return
        }
        setMaxHeight(`${full}px`)
        return
      }

      if (phase !== "measuring") return
      // The clamps are back on for this commit and the pin still holds the
      // full height, so what the element measures now is the height the
      // clamped content has today — fresh, whatever the window or the fonts
      // did while the card was open.
      const target = node.clientHeight
      if (Math.abs(closingFrom.current - target) <= 1) {
        // Equal heights: the same rule as above, for the same reason. The
        // panel settles shut as it is, and the clamps stay on.
        node.style.maxHeight = ""
        setPhase("collapsed")
        setMaxHeight(undefined)
        return
      }
      setPhase("closing")
      setMaxHeight(`${target}px`)
    }, [phase, measuredPeek])

    const handleTransitionEnd = (
      event: React.TransitionEvent<HTMLDivElement>,
    ) => {
      if (event.propertyName !== "max-height") return
      if (phase === "opening") {
        setPhase("open")
        setMaxHeight(undefined)
      } else if (phase === "closing") {
        setPhase("collapsed")
        if (measuredPeek) {
          // The content's own clamps take over from here; keeping the
          // measured height would clip them after any later reflow.
          setMaxHeight(undefined)
        }
      }
    }

    /**
     * Whether the content is laid out with its own clamps. That is the settled
     * shut state — and the one commit, never painted, in which the clamps are
     * put back on to measure what they leave. While the height is moving —
     * either way — the content is laid out in full and clipped by the
     * max-height instead.
     */
    const clamped = phase === "collapsed" || phase === "measuring"
    const clipped = phase === "collapsed" || phase === "closing"

    return (
      <div
        ref={setRefs}
        onTransitionEnd={handleTransitionEnd}
        data-state={open ? "open" : "collapsed"}
        style={{ maxHeight }}
        className={cn(
          "relative overflow-hidden transition-[max-height] duration-300 ease-out motion-reduce:transition-none",
          !measuredPeek &&
            fade &&
            clipped &&
            "after:pointer-events-none after:absolute after:inset-x-0 after:bottom-0 after:h-6 after:bg-gradient-to-b after:from-transparent after:to-card",
          className,
        )}
        {...rest}
      >
        {typeof children === "function" ? children(clamped) : children}
      </div>
    )
  },
)

Expandable.displayName = "Expandable"
