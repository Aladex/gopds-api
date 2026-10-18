# Send to Kindle — how-to videos

Builds `booksdump-frontend/public/videos/kindle-setup-{ru,en}.mp4` and their posters
`kindle-setup-{ru,en}.jpg`: silent ~63 s walkthroughs with burned-in captions.

## Rebuild

```sh
(cd booksdump-frontend && yarn install)            # once: the real UI is recorded from vite dev
cd scripts/kindle-video && npm ci && node build.mjs  # records and encodes both languages
```

Needs `ffmpeg` on `PATH` and Playwright's Chromium (`npx playwright install chromium` if
`~/.cache/ms-playwright` does not have the build matching `playwright@1.62.1`).

Options: `--lang ru` (repeatable) for one language, `--skip-record` to re-encode the last
recording in `out/` (captions, zoom, CRF changes), `--vite-url http://127.0.0.1:3000` to use a
dev server that is already running instead of starting one on port 3417.

`out/` (not committed) keeps the raw frames, per-scene clips, `keyframes-{ru,en}.png` contact
sheets (one frame per caption) and `summary-{ru,en}.json` (duration, size, CRF, caption timings).

## How it works

- **Scenes** (`scenes/`): an opening beat and five steps written in a few verbs from `lib/shot.mjs` — `caption`,
  `move`, `click`, `zoom`, `scrollTo`, `keys`, `hold`. A caption stays up at least 2.6 s per
  line (longer for long lines) whatever the scene does.
  - opening, 1, 4, 5 — the real booksdump UI from `vite` dev: muted Kindle button → setup
    dialog → "Open Kindle settings" → profile; Copy; paste + Save; card button → toast from the
    websocket reply. It is served as `https://booksdump.com` inside the browser, so addresses it
    derives from its location read right. Every `/api` call (including `PUT`/`DELETE
    /api/books/kindle`) and the `/api/ws` socket are answered by `lib/mocks.mjs`; nothing leaves
    the machine.
  - 2, 3 — `amazon/mycd.html`, a neutral stylised stand-in for Amazon's "Manage Your Content and
    Devices" with the real page's tab, section, link and button labels. No Amazon logo or artwork.
- **Recording** (`lib/recorder.mjs`): Chrome's screencast at 2× (2560×1440) with the wall-clock
  time of every frame, plus a log of captions, zooms and markers. `lib/overlay.js` draws the
  cursor, click ripples and key-cap badges inside the page.
- **Encoding** (`lib/encode.mjs`): frames → 30 fps per scene with the logged zooms applied
  (`zoompan`, eased), scenes cross-faded, caption PNGs (`lib/caption-images.mjs`, Onest)
  faded in and out, then H.264 High / yuv420p / `+faststart`, 1280×720, silent. The CRF starts
  at 22 and steps up until the file is ≤ 4 MB.

Caption text lives in `captions.mjs`; the fixture user, books and addresses in `lib/mocks.mjs`.

## Things to know when re-cutting

- Copy and paste are real: the context has clipboard permissions, scene 1 presses the profile's
  Copy, scene 3 pastes with Ctrl+V into the Amazon mock and copies the Kindle address back,
  scene 4 pastes it into the profile. Each paste is asserted, so a broken step fails the build.
- The setup dialog (opening scene) plays `public/videos/kindle-setup-*.mp4` with its poster, i.e.
  the previous build. After a change to scene 1, run the build twice so the poster inside the
  dialog shows the new cut.
- Control names come from the frontend's locales (`L` in `scenes/booksdump.mjs`); if a label
  changes there, change it here too.
