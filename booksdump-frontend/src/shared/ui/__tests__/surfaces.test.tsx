import React from "react"
import { render } from "@testing-library/react"
import { readFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

import { Drawer, DrawerContent, DrawerTitle } from "@/shared/ui/drawer"
import { Dialog, DialogContent, DialogTitle } from "@/shared/ui/dialog"
import { Button } from "@/shared/ui/button"
import { Switch } from "@/shared/ui/switch"

// In the light theme the page background is the grey desk (#b3b3b3) and the
// card/popover colour is the paper. Everything raised over the page — sheets,
// dialogs, their footers and the controls on them — is made of paper; a sheet
// of desk grey made its fields look switched off.

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

// sRGB relative luminance and contrast, as WCAG defines them.
const luminance = (channel: number) => {
  const c = channel / 255
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}
const contrast = (a: number, b: number) => {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

it("draws a field’s outline at 3:1 or more on paper and on the desk", () => {
  // The light palette is the block that sets the desk grey; the dark one
  // follows it, so the light block ends where that one begins.
  const from = css.indexOf("--background: #b3b3b3")
  expect(from).toBeGreaterThan(-1)
  const light = css.slice(from, css.indexOf("--background: #121212"))
  const alpha = Number(
    light.match(/--input:\s*rgba\(0,\s*0,\s*0,\s*([\d.]+)\)/)![1],
  )
  const over = (base: number) => Math.round(base * (1 - alpha))

  expect(contrast(over(255), 255)).toBeGreaterThanOrEqual(3)
  expect(contrast(over(0xb3), 0xb3)).toBeGreaterThanOrEqual(3)
  // The page background itself is not this change's to touch.
  expect(light).toMatch(/--background:\s*#b3b3b3/)
})

it("draws a dark field's outline at 3:1 or more on a dark sheet and the dark page", () => {
  const dark = css.slice(css.indexOf("--background: #121212"))
  const alpha = Number(
    dark.match(/--input:\s*rgba\(255,\s*255,\s*255,\s*([\d.]+)\)/)![1],
  )
  const over = (base: number) => Math.round(base + (255 - base) * alpha)

  // #1e1e1e is the dark card/popover — every sheet and dialog — and #121212
  // the dark page.
  expect(contrast(over(0x1e), 0x1e)).toBeGreaterThanOrEqual(3)
  expect(contrast(over(0x12), 0x12)).toBeGreaterThanOrEqual(3)
})

// The fills made from the dark input token were rescaled with it: a field, a
// checkbox, an outline button and a switch track keep the shades they had at
// 0.12 with /30, /50 and /80. Only the outlines were meant to change.
it("keeps the dark fills made from the input token at their old shades", () => {
  const dark = css.slice(css.indexOf("--background: #121212"))
  const alpha = Number(
    dark.match(/--input:\s*rgba\(255,\s*255,\s*255,\s*([\d.]+)\)/)![1],
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
