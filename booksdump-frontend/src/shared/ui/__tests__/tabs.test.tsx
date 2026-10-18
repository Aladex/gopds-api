import React from "react"
import { render, screen } from "@testing-library/react"
import { Plug, UserRound } from "lucide-react"

import { Tabs, TabsList, TabsTrigger } from "@/shared/ui/tabs"

// The app's one tab component, in its two looks and the phone's stacked
// layout. jsdom lays nothing out, so these hold the classes that do the work;
// the sizes they produce were measured in Chromium (report-post3.md).

const renderTabs = (list: React.ComponentProps<typeof TabsList> = {}) =>
  render(
    <Tabs defaultValue="profile">
      <TabsList {...list}>
        <TabsTrigger value="profile" icon={UserRound}>
          Профиль
        </TabsTrigger>
        <TabsTrigger value="connections" icon={Plug}>
          Подключения
        </TabsTrigger>
      </TabsList>
    </Tabs>,
  )

describe("the segmented bar", () => {
  it("puts the icon beside the label, never cuts the label, and outlines the chosen segment", () => {
    renderTabs()
    const tab = screen.getByRole("tab", { name: "Профиль" })

    expect(screen.getByRole("tablist")).toHaveAttribute(
      "data-variant",
      "segmented",
    )
    expect(tab.className).toMatch(/whitespace-nowrap/)
    expect(tab.className).not.toMatch(/truncate|text-ellipsis/)
    expect(tab.className).not.toMatch(/flex-col/)
    // The outline is what tells the chosen segment from the rail.
    expect(tab).toHaveAttribute("data-state", "active")
    expect(tab.className).toMatch(/data-\[state=active\]:border-foreground\/50/)
    // Over the rail, not over its own fill: only then does it reach 3:1.
    expect(tab.className).toMatch(/\bbg-clip-padding\b/)
    const icon = tab.querySelector("svg")!
    expect(icon).toHaveAttribute("aria-hidden", "true")
    expect(tab.firstElementChild).toBe(icon)
  })

  it("stacks the icon over the label in content-width columns, tall enough for a thumb", () => {
    renderTabs({ stacked: true })
    const tab = screen.getByRole("tab", { name: "Подключения" })

    expect(screen.getByRole("tablist")).toHaveAttribute("data-stacked", "true")
    expect(tab.className).toMatch(/\bflex-col\b/)
    // Content-width, sharing the spare room: equal thirds cut «Подключения».
    expect(tab.className).toMatch(/\bflex-auto\b/)
    expect(tab.className).not.toMatch(/\bflex-1\b|\bmin-w-0\b/)
    expect(tab.className).toMatch(/\bmin-h-12\b/)
    expect(tab.className).toMatch(/text-\[13px\]/)
    expect(tab.className).not.toMatch(/truncate/)
  })
})

describe("the underline bar", () => {
  it("draws the chosen tab with a line, at least 44px to press, and never stacks", () => {
    renderTabs({ variant: "underline", stacked: true })
    const tab = screen.getByRole("tab", { name: "Профиль" })

    expect(screen.getByRole("tablist")).toHaveAttribute(
      "data-variant",
      "underline",
    )
    expect(screen.getByRole("tablist")).not.toHaveAttribute("data-stacked")
    expect(screen.getByRole("tablist").className).not.toMatch(/bg-muted/)
    expect(tab.className).toMatch(/\bmin-h-11\b/)
    expect(tab.className).toMatch(/border-b-2/)
    expect(tab.className).toMatch(
      /data-\[state=active\]:border-foreground(\s|$)/,
    )
    expect(tab.className).not.toMatch(/flex-col/)
  })
})
