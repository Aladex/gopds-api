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
