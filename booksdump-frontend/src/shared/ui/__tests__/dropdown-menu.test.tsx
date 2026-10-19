import React from "react"
import {
  act,
  createEvent,
  fireEvent,
  render,
  screen,
} from "@testing-library/react"
import userEvent from "@testing-library/user-event"

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/shared/ui/dropdown-menu"

// Every dropdown in the app opens through this trigger. A finger that lands on
// it to scroll the page must not open it; a tap, a mouse and a keyboard must.

const renderMenu = (onOpenChange?: (open: boolean) => void) =>
  render(
    <DropdownMenu modal={false} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger>menu</DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem>item</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>,
  )

const trigger = () => screen.getByRole("button", { name: "menu" })
const isOpen = () => trigger().getAttribute("aria-expanded") === "true"

// jsdom's PointerEvent may not carry pointerType through its init, so it is set
// on the event itself.
const pointer = (
  type: "pointerDown" | "pointerCancel" | "pointerUp",
  pointerType: string,
) => {
  const event = createEvent[type](trigger(), { button: 0, ctrlKey: false })
  Object.defineProperty(event, "pointerType", { value: pointerType })
  act(() => {
    fireEvent(trigger(), event)
  })
}

describe("the dropdown trigger", () => {
  it("does not open for a touch that turns into a scroll", () => {
    renderMenu()
    pointer("pointerDown", "touch")
    expect(isOpen()).toBe(false)
    // The browser takes the gesture for a scroll: no click follows.
    pointer("pointerCancel", "touch")
    expect(isOpen()).toBe(false)
    // A stray click with no tap behind it (assistive tech) does not inherit
    // the cancelled touch.
    fireEvent.click(trigger())
    expect(isOpen()).toBe(false)
  })

  it("opens on a tap's click and closes on the next tap", () => {
    renderMenu()
    pointer("pointerDown", "touch")
    expect(isOpen()).toBe(false)
    pointer("pointerUp", "touch")
    fireEvent.click(trigger())
    expect(isOpen()).toBe(true)

    pointer("pointerDown", "pen")
    pointer("pointerUp", "pen")
    fireEvent.click(trigger())
    expect(isOpen()).toBe(false)
  })

  it("still opens on a mouse press, as Radix does", () => {
    const changes: boolean[] = []
    renderMenu((open) => changes.push(open))
    pointer("pointerDown", "mouse")
    expect(isOpen()).toBe(true)
    // The mouse's own click does not toggle it a second time.
    fireEvent.click(trigger())
    expect(isOpen()).toBe(true)
    expect(changes).toEqual([true])
  })

  it.each(["{Enter}", " ", "{ArrowDown}"])(
    "opens from the keyboard with %s",
    async (key) => {
      renderMenu()
      trigger().focus()
      await userEvent.keyboard(key)
      expect(isOpen()).toBe(true)
      expect(
        await screen.findByRole("menuitem", { name: "item" }),
      ).toBeInTheDocument()
    },
  )
})
