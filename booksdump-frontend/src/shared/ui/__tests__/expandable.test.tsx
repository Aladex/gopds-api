import { readFileSync } from "node:fs"
import { resolve } from "node:path"

import React from "react"
import { render, screen, act } from "@testing-library/react"

import { Expandable } from "@/shared/ui/expandable"

// The point of this component is that it animates where the obvious techniques
// cannot. jsdom computes no real layout, so what is asserted here is the state
// machine that makes the animation possible: an explicit pixel height to
// transition between, released once open so later reflows are not clipped, and
// pinned again before closing so there is a value to animate down from.

const LONG = "Аннотация ".repeat(60)

function setReducedMotion(reduce: boolean) {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: query.includes("prefers-reduced-motion") ? reduce : false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia
}

/**
 * recordMaxHeights collects every max-height the element is given, in order.
 *
 * The pin that makes the closing transition possible is written and then
 * superseded within the same tick, so it cannot be read off the final style —
 * but it is what the browser lays out against, and its absence is exactly the
 * bug where the panel snapped shut. The sequence is the only place it shows.
 */
function recordMaxHeights(el: HTMLElement): string[] {
  const maxHeightOf = (style: string | null) =>
    /max-height:\s*([^;]+)/.exec(style ?? "")?.[1].trim() ?? ""

  const seen: string[] = []
  const observer = new MutationObserver((records) => {
    // Records arrive in a batch, by which time the element already holds
    // the last value — so the history comes from the records themselves.
    for (const record of records) seen.push(maxHeightOf(record.oldValue))
    seen.push(el.style.maxHeight)
  })
  observer.observe(el, {
    attributes: true,
    attributeFilter: ["style"],
    attributeOldValue: true,
  })
  return seen
}

/** endTransition fires the event the component waits for. */
function endTransition(el: HTMLElement) {
  act(() => {
    el.dispatchEvent(
      Object.assign(new Event("transitionend", { bubbles: true }), {
        propertyName: "max-height",
      }),
    )
  })
}

beforeEach(() => {
  setReducedMotion(false)
})

describe("Expandable", () => {
  it("reports its state so styling and tests can key on it", () => {
    const { rerender } = render(
      <Expandable open={false} data-testid="box">
        {LONG}
      </Expandable>,
    )

    expect(screen.getByTestId("box")).toHaveAttribute("data-state", "collapsed")

    rerender(
      <Expandable open data-testid="box">
        {LONG}
      </Expandable>,
    )

    expect(screen.getByTestId("box")).toHaveAttribute("data-state", "open")
  })

  it("clips to a peek height while collapsed", () => {
    render(
      <Expandable open={false} peekLines={2} data-testid="box">
        {LONG}
      </Expandable>,
    )

    // In the element's own line heights, so the very first paint is right
    // without measuring — measuring costs a frame of unclamped content.
    expect(screen.getByTestId("box").style.maxHeight).toBe("2lh")
  })

  it("arrives in the state it was asked for without animating into it", () => {
    // Every card in a freshly loaded list used to unfold and fold itself
    // back up, because the collapse was applied after the first paint.
    const { unmount } = render(
      <Expandable open={false} peekLines={2} data-testid="box">
        {LONG}
      </Expandable>,
    )

    const box = screen.getByTestId("box")
    expect(box.style.maxHeight).toBe("2lh")
    expect(box).toHaveAttribute("data-state", "collapsed")
    unmount()

    render(
      <Expandable open peekLines={2} data-testid="open-box">
        {LONG}
      </Expandable>,
    )

    // Open on arrival means no clamp at all, not a clamp released later.
    expect(screen.getByTestId("open-box").style.maxHeight).toBe("")
  })

  it("animates towards an explicit height, not towards auto", () => {
    const { rerender } = render(
      <Expandable open={false} data-testid="box">
        {LONG}
      </Expandable>,
    )

    rerender(
      <Expandable open data-testid="box">
        {LONG}
      </Expandable>,
    )

    // A height in pixels is what makes the transition possible at all.
    expect(screen.getByTestId("box").style.maxHeight).toMatch(/px$/)
  })

  it("releases the height once open so later reflows are not clipped", () => {
    const { rerender } = render(
      <Expandable open={false} data-testid="box">
        {LONG}
      </Expandable>,
    )
    rerender(
      <Expandable open data-testid="box">
        {LONG}
      </Expandable>,
    )

    endTransition(screen.getByTestId("box"))

    expect(screen.getByTestId("box").style.maxHeight).toBe("")
  })

  it("pins the height again before closing", async () => {
    const { rerender } = render(
      <Expandable open peekLines={2} data-testid="box">
        {LONG}
      </Expandable>,
    )
    endTransition(screen.getByTestId("box"))
    expect(screen.getByTestId("box").style.maxHeight).toBe("")

    const seen = recordMaxHeights(screen.getByTestId("box"))

    rerender(
      <Expandable open={false} peekLines={2} data-testid="box">
        {LONG}
      </Expandable>,
    )
    // The observer delivers on a microtask; let it.
    await act(async () => {})

    // A measured height first, the peek second. Going straight to the peek
    // gives the browser one value to compute and no transition to run.
    const pin = seen.findIndex((value) => /px$/.test(value))
    expect(
      pin,
      `expected a measured pin in ${JSON.stringify(seen)}`,
    ).toBeGreaterThanOrEqual(0)
    expect(seen.indexOf("2lh")).toBeGreaterThan(pin)
  })

  it("keeps the content mounted while collapsed, so it stays searchable", () => {
    render(<Expandable open={false}>полный текст аннотации</Expandable>)

    expect(screen.getByText("полный текст аннотации")).toBeInTheDocument()
  })

  it("skips the transition when the reader asked for less motion", () => {
    setReducedMotion(true)
    const { rerender } = render(
      <Expandable open={false} data-testid="box">
        {LONG}
      </Expandable>,
    )

    rerender(
      <Expandable open data-testid="box">
        {LONG}
      </Expandable>,
    )

    // Straight to released height, no intermediate pixel target.
    expect(screen.getByTestId("box").style.maxHeight).toBe("")
    expect(screen.getByTestId("box")).toHaveAttribute("data-state", "open")
  })
})

/*
 * The measured peek: the content clamps itself — line-clamp, truncate, one
 * fixed row — and keeps the ellipsis that comes with those clamps, while this
 * component animates between the height the clamped content measures and the
 * height of the full content. The content learns when to clamp through the
 * render prop; jsdom still lays nothing out, so the two heights are handed to
 * the element directly.
 */

const PEEK = 60
const FULL = 120

/** Gives the element the two heights the animation runs between. */
function setHeights(el: HTMLElement, visible: number, content: number) {
  for (const [name, value] of [
    ["clientHeight", visible],
    ["scrollHeight", content],
  ] as const) {
    Object.defineProperty(el, name, { configurable: true, get: () => value })
  }
}

/** The content as the card writes it: its own clamp while it is held shut. */
const selfClamping = (clamped: boolean) => (
  <p
    data-testid="text"
    data-clamped={String(clamped)}
    className={clamped ? "line-clamp-3" : ""}
  >
    {LONG}
  </p>
)

const clampedNow = () =>
  screen.getByTestId("text").getAttribute("data-clamped") === "true"

describe("Expandable with a measured peek", () => {
  it("leaves the collapsed height to the content's own clamps", () => {
    render(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    const box = screen.getByTestId("box")
    setHeights(box, PEEK, FULL)

    // No clamp of its own: the first paint is the clamped content as the
    // content itself styled it, with nothing to correct afterwards.
    expect(box.style.maxHeight).toBe("")
    expect(clampedNow()).toBe(true)
    expect(screen.getByTestId("text")).toHaveClass("line-clamp-3")
  })

  it("arrives open with the content unclamped and nothing measured", () => {
    render(
      <Expandable open measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )

    expect(screen.getByTestId("box").style.maxHeight).toBe("")
    expect(clampedNow()).toBe(false)
  })

  it("opens from a measured pin to a measured target", async () => {
    const { rerender } = render(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    const box = screen.getByTestId("box")
    setHeights(box, PEEK, FULL)
    await act(async () => {})

    const seen = recordMaxHeights(box)
    rerender(
      <Expandable open measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})

    // The height it opens from is the one on screen — the clamped content —
    // and the one it opens to is the full content, both in pixels, because
    // those are the only two values a transition can run between.
    expect(seen.indexOf(`${PEEK}px`)).toBeGreaterThanOrEqual(0)
    expect(seen.indexOf(`${FULL}px`)).toBeGreaterThan(seen.indexOf(`${PEEK}px`))
    // The clamp comes off as the height starts moving, not after it lands.
    expect(clampedNow()).toBe(false)

    endTransition(box)
    expect(box.style.maxHeight).toBe("")
  })

  it("closes by pinning the full height before moving", async () => {
    const { rerender } = render(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    const box = screen.getByTestId("box")
    setHeights(box, PEEK, FULL)
    await act(async () => {})
    rerender(
      <Expandable open measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})
    endTransition(box)
    expect(box.style.maxHeight).toBe("")

    const seen = recordMaxHeights(box)
    rerender(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})

    // The full height first, the clamped one second: max-height can only
    // clip, never hold anything open, so without the pin the re-clamped
    // content would land at once.
    expect(seen.indexOf(`${FULL}px`)).toBeGreaterThanOrEqual(0)
    expect(seen.indexOf(`${PEEK}px`)).toBeGreaterThan(seen.indexOf(`${FULL}px`))
    // While the height is still moving the content stays unclamped — the
    // clamp returns only once the motion is done.
    expect(clampedNow()).toBe(false)

    endTransition(box)
    expect(box.style.maxHeight).toBe("")
    expect(clampedNow()).toBe(true)
    expect(screen.getByTestId("text")).toHaveClass("line-clamp-3")
  })

  it("settles at once when nothing is actually held back", async () => {
    // A transition between two equal heights never ends, so waiting for it
    // would leave the panel stuck halfway through its own state machine.
    const { rerender } = render(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    const box = screen.getByTestId("box")
    setHeights(box, FULL, FULL)
    await act(async () => {})

    rerender(
      <Expandable open measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})

    expect(box.style.maxHeight).toBe("")
    expect(clampedNow()).toBe(false)
    expect(box).toHaveAttribute("data-state", "open")
  })

  it("skips the measured transition when the reader asked for less motion", async () => {
    setReducedMotion(true)
    const { rerender } = render(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    const box = screen.getByTestId("box")
    setHeights(box, PEEK, FULL)
    await act(async () => {})

    rerender(
      <Expandable open measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})

    expect(box.style.maxHeight).toBe("")
    expect(clampedNow()).toBe(false)
  })

  it("drives its phases only in layout effects, so no frame can catch the measuring commit", () => {
    // jsdom cannot observe this at runtime: act() flushes passive effects
    // after every commit, which erases exactly the boundary the rule is
    // about — in a real browser, a passive restore could let the browser
    // paint the clamps-on measuring commit before the close starts moving.
    // The contract is asserted on the source instead (the pattern the
    // breakpoints spec uses for architecture rules): every effect that moves
    // the phase machine must run before paint.
    const source = readFileSync(resolve(__dirname, "../expandable.tsx"), "utf8")
    expect(source).not.toMatch(/React\.useEffect/)
    expect(source.match(/React\.useLayoutEffect/g)).toHaveLength(2)
  })

  it("closes onto the height the clamped content has now, not the one it had when it opened", async () => {
    // The window narrows while the card is open: both heights move. Closing
    // onto the remembered one would land beside the clamped content and jump
    // when the clamps return.
    const { rerender } = render(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    const box = screen.getByTestId("box")
    setHeights(box, PEEK, FULL)
    await act(async () => {})
    rerender(
      <Expandable open measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})
    endTransition(box)

    // The clamped flag the content renders by, recorded across the close: it
    // must come back on for the measuring commit — before the motion, not
    // just after it — or the "clamped" height is nothing of the kind.
    const clampedSequence: string[] = []
    const text = screen.getByTestId("text")
    const clampedObserver = new MutationObserver((records) => {
      // Records arrive in a batch, by which time the attribute holds the
      // last value — the history comes from the records themselves.
      for (const record of records) clampedSequence.push(record.oldValue ?? "")
      clampedSequence.push(text.getAttribute("data-clamped") ?? "")
    })
    clampedObserver.observe(text, {
      attributes: true,
      attributeFilter: ["data-clamped"],
      attributeOldValue: true,
    })

    const WIDER = 80
    const TALLER = 140
    setHeights(box, WIDER, TALLER)
    const seen = recordMaxHeights(box)
    rerender(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})

    expect(seen.indexOf(`${TALLER}px`)).toBeGreaterThanOrEqual(0)
    expect(seen.indexOf(`${WIDER}px`)).toBeGreaterThan(
      seen.indexOf(`${TALLER}px`),
    )
    // true for the measurement, then false again for the motion — the record
    // begins with the open state's own false, so it is the last false that
    // has to come after the measurement's true.
    expect(clampedSequence).toContain("true")
    expect(clampedSequence.lastIndexOf("false")).toBeGreaterThan(
      clampedSequence.indexOf("true"),
    )

    endTransition(box)
    expect(box.style.maxHeight).toBe("")
    expect(clampedNow()).toBe(true)
  })

  it("settles shut at once when nothing would move, rather than waiting for a transition that never ends", async () => {
    // A transition between two equal heights fires no transitionend, so an
    // unconditional close would leave the panel stuck in its closing phase —
    // unclamped, with a measured height holding it.
    const { rerender } = render(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    const box = screen.getByTestId("box")
    setHeights(box, FULL, FULL)
    await act(async () => {})
    rerender(
      <Expandable open measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})
    expect(box).toHaveAttribute("data-state", "open")

    rerender(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})

    expect(box.style.maxHeight).toBe("")
    expect(clampedNow()).toBe(true)
  })

  it("closes measured, too, when it arrived open and never saw the clamped content", async () => {
    const { rerender } = render(
      <Expandable open measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    const box = screen.getByTestId("box")
    setHeights(box, PEEK, FULL)
    await act(async () => {})
    expect(box.style.maxHeight).toBe("")

    const seen = recordMaxHeights(box)
    rerender(
      <Expandable open={false} measuredPeek data-testid="box">
        {selfClamping}
      </Expandable>,
    )
    await act(async () => {})

    expect(seen.indexOf(`${FULL}px`)).toBeGreaterThanOrEqual(0)
    expect(seen.indexOf(`${PEEK}px`)).toBeGreaterThan(seen.indexOf(`${FULL}px`))

    endTransition(box)
    expect(box.style.maxHeight).toBe("")
    expect(clampedNow()).toBe(true)
  })
})
