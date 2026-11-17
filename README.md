# scry

A desktop front end for [scrcpy](https://github.com/Genymobile/scrcpy), built on
giokit. It lists your adb devices, mirrors them **inside its own window**
(scry speaks the scrcpy protocol itself) or launches scrcpy, and maps keys to
touches for games.

```sh
go run .                          # your adb devices
go run . -demo                    # plus a pretend device streaming a test pattern
go run . -demo-file clip.mp4      # plus a pretend device streaming clip.mp4
go run . -demo -start -v          # start mirroring at once, logs on stderr
go test ./...                     # headless; SCRY_SHOTS=dir saves screenshots
```

Needs `adb` and an installed `scrcpy` (its server is pushed to the device, so
client and server versions always match) and FFmpeg's libraries (scrcpy needs
them too).

## What it does

- **Devices:** listed from adb with their real names ("mira", Galaxy Tab S9
  Ultra · Android 16). Unauthorized devices say what to do.
- **Settings:** the scrcpy options grouped by what they affect, per device, with
  the generated command line shown live. Session kinds (Mirror, App window,
  Camera, Audio only, Keyboard & mouse) show only what applies.
- **Mirror inside scry** (Mirror, App window, Camera): the Screen tab shows the
  device. On Linux with VA-API the video is decoded on the GPU and drawn
  without leaving it (zero copy); otherwise in software. Mouse, keyboard
  (scrcpy's typing modes), scroll, Ctrl+drag pinch, clipboard both ways, and
  toolbar buttons (Back, Home, Recents, volume, rotate, screen off). Audio plays
  through a second, video-less scrcpy.
- **Game controls:** buttons and WASD joysticks placed on the video; keys
  become touches (multi-touch). Edit them on the video: drag to move, select and
  press a key to bind. Layouts are saved per device in
  `~/.config/scry/keymaps.json`.
- **Separate window:** Settings → Window → "Open in a separate scrcpy window"
  launches scrcpy itself (needed for recording, v4l2, OTG, audio-only).
- **Logs:** everything scrcpy, the server and adb print, per device, filterable
  by level and text, with an adb command box.

## Layout

| Package | What |
|---|---|
| `scrcpy` | `Options` (a session as plain data), `Args` for the scrcpy CLI, running scrcpy, log parsing |
| `scrcpy/proto` | the scrcpy 5.0 wire protocol: stream framing, control and device messages (tested against scrcpy's own golden vectors) |
| `scrcpy/client` | runs a session: push the server, reverse (or forward) tunnel, launch, read video, send control |
| `scrcpy/input` | keyboard (scrcpy's mixed/text/raw rules), mouse (scrcpy's default bindings, pinch) and game-control keymaps |
| `scrcpy/fake` | a pretend device serving a video file over the protocol: tests and `-demo` |
| `adb` | device listing and names |
| `ui` | the giokit front end; `settings.go` is the settings schema, `screen.go` the embedded view, `keymap.go` the game-controls editor |
| `docs/options.md` | every scrcpy 5.0 flag, categorised, with the rules between them |

Video decoding and drawing live in giokit (`giokit/media`, `paint.Stream`,
`ui.Video`; see giokit's `docs/design/video.md`).

## Environment

| Variable | Effect |
|---|---|
| `ADB` | adb executable |
| `SCRCPY_SERVER_PATH` | scrcpy-server to push, instead of the installed scrcpy's |
| `SCRY_HWDEC=off` | decode in software (for a misbehaving GPU driver) |
| `GIOKIT_TRACE=1` | frame pacing on stderr |

## Testing without a device

Everything that needs a phone has a stand-in: `scrcpy/fake` serves video over
the real protocol (and records input), and `scrcpy/client`'s tests run
`Connect` against a fake `adb` script that launches the test binary as the
server, through both tunnel modes. The UI tests drive the embedded view with
the demo device: clicks become touches at the right device coordinates, keys
and text, toolbar, game controls and the layout editor.

Not testable here: the real device side (scrcpy-server on a phone), and audio.

## Measured

2960×1848 at 60 fps, RX 6700 XT, Wayland: locked 60 fps, a whole scry frame
builds in 0.13 ms, about 12% of one core with zero copy versus 75% decoding in
software.
