import React from "react"
import { render } from "@testing-library/react"
import { readFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

import { Drawer, DrawerContent, DrawerTitle } from "@/shared/ui/drawer"
import { Dialog, DialogContent, DialogTitle } from "@/shared/ui/dialog"
import { Button } from "@/shared/ui/button"
import { Switch } from "@/shared/ui/switch"

// In the light theme the page background is the desk and the card/popover
// colour is the lighter paper laid on it. Everything raised over the page —
// sheets, dialogs, their footers and the controls on them — is made of paper;
// a sheet of the desk's colour made its fields look switched off.

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../../index.css"),
  "utf8",
)

beforeAll(() => {
  Element.prototype.setPointerCapture ??= () => {}
  Element.prototype.releasePointerCapture ??= () => {}
  Element.prototype.hasPointerCapture ??= () => false
})

it("makes sheets and dialogs of the same paper", () => {
  render(
    <Drawer open>
      <DrawerContent>
        <DrawerTitle>sheet</DrawerTitle>
      </DrawerContent>
    </Drawer>,
  )
  render(
    <Dialog open>
      <DialogContent closeLabel="close">
        <DialogTitle>dialog</DialogTitle>
      </DialogContent>
    </Dialog>,
  )

  for (const slot of ["drawer-content", "dialog-content"]) {
    const surface = document.querySelector(`[data-slot="${slot}"]`)!
    expect(surface.className).toMatch(/\bbg-popover\b/)
    expect(surface.className).not.toMatch(/\bbg-background\b/)
  }
})

it("lets outline buttons and switch thumbs take the surface they sit on", () => {
  const { getByRole } = render(
    <>
      <Button variant="outline">copy</Button>
      <Switch aria-label="switch" />
    </>,
  )

  expect(getByRole("button", { name: "copy" }).className).not.toMatch(
    /\bbg-background\b/,
  )
  const thumb = getByRole("switch").querySelector('[data-slot="switch-thumb"]')!
  expect(thumb.className).toMatch(/\bbg-card\b/)
})

// A field's outline at 3:1 on every surface, in both themes, is checked with
// the rest of the palette in src/__tests__/palette.

// The fills made from the dark input token were rescaled with it: a field, a
// checkbox, an outline button and a switch track keep the shades they had at
// 0.12 with /30, /50 and /80. Only the outlines were meant to change.
it("keeps the dark fills made from the input token at their old shades", () => {
  const dark = css.slice(css.indexOf("[data-theme='dark'] {"))
  const alpha = Number(
    dark.match(/--input:\s*rgba\(255,\s*240,\s*210,\s*([\d.]+)\)/)![1],
  )
  const sources = [
    "input",
    "checkbox",
    "button",
    "select",
    "textarea",
    "switch",
  ]
    .map((name) =>
      readFileSync(
        join(dirname(fileURLToPath(import.meta.url)), `../${name}.tsx`),
        "utf8",
      ),
    )
    .join("\n")
  const used = [...sources.matchAll(/bg-input\/(\d+)/g)].map((m) =>
    Number(m[1]),
  )
  expect(used.length).toBeGreaterThan(0)
  const before = [0.12 * 0.3, 0.12 * 0.5, 0.12 * 0.8]
  for (const percent of used) {
    const shade = alpha * (percent / 100)
    expect(before.some((old) => Math.abs(old - shade) < 0.004)).toBe(true)
  }
})
