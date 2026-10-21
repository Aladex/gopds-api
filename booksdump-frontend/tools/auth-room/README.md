# Reading-room videos

The sign-in screens play a looping recording of the reading room
(`src/features/auth/room/RoomVideo.tsx`). This folder makes those recordings
from the real three.js scene. None of it ships with the app: `three` is a
devDependency, and nothing under `src/` imports from here.

## What is here

- `scene/` — the scene itself (book, physics, typesetting, the WebGL glue) and
  its unit tests, which run with the app's `yarn test`.
- `public/auth-scene/` — the textures, HDR and logo cut-outs the scene renders
  with (credits in `public/auth-scene/CREDITS.md`).
- `recorder/` — the page the recorder drives: seeded randomness, the scene in
  record mode, one fixed step per frame, the drawing buffer read back as PNG.
- `record.mjs` — builds that page with Vite, drives it in Chromium on the GPU,
  assembles the loop and encodes it with ffmpeg.

## Re-rendering

Needs ffmpeg (libvpx-vp9, libx264, libwebp) and a Chromium that can use the
machine's GPU — not SwiftShader.

```sh
cd booksdump-frontend
AUTH_ROOM_CHROME=/path/to/chromium node tools/auth-room/record.mjs            # all four
AUTH_ROOM_CHROME=/path/to/chromium node tools/auth-room/record.mjs phone-torn # one
AUTH_ROOM_MARKERS_ONLY=1 node tools/auth-room/record.mjs                       # markers only
```

It writes, per variant (`desktop`, `desktop-torn` at 1920×1080; `phone`,
`phone-torn` at 1080×1920):

- `public/auth-room/<variant>.webm` (VP9, two-pass) and `.mp4` (H.264 High,
  `+faststart`), each at the lowest CRF that stays under the budget (3 MB
  desktop, 2.5 MB phone);
- `public/auth-room/<variant>.webp`, the poster — the loop's first frame;
- the Easter eggs' positions in the frame (golden post on the table, the
  pupils, the mask's box) into `src/features/auth/room/markers.json`, one
  entry per composition. A run that renders only some variants replaces only
  their composition's entry and keeps the other; it stops if a variant
  rendered alone no longer agrees with the kept entry (re-render both variants
  of that composition then), or if a composition would be left without one.

Frames, the lossless master and a report (sizes, CRF, bitrates, SSIM of the
loop seam against neighbouring frames) go to `tools/auth-room/work/`, which is
not committed.

## How the loop closes

24 s of loop plus 2 s are rendered at 30 fps after a 3 s warm-up. The first
2 s of the output crossfade from the frames _after_ the loop into its first
frames, so the video's last frame is followed, when it wraps, by the very next
frame the scene rendered: the seam is never a blend, the blend is inside. The
scripted gusts (three slow sweeps of a "hand") stay clear of both crossfade
windows. The report's `seamSsim` should sit with its `neighbourSsim`.

The same seed (`AUTH_ROOM_SEED`, default 7) gives the same video frame for
frame.
