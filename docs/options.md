# scrcpy front end: option catalog and UX structure

Source of truth: `scrcpy --help` and the validation strings in the binary, scrcpy 5.0.1
(Arch). All 118 flags are accounted for below. Tier: **Q** = quick (on the device page),
**S** = standard (settings section), **A** = advanced (behind the section's "Advanced"
disclosure), **—** = never shown (the app owns it).

## 0. Owned by the app (never a user-facing option)

| Flag | Why hidden |
|---|---|
| `-s/--serial`, `-d`, `-e`, `--tcpip[=...]` | Device selection and connection belong to the device manager. Always pass `-s`. |
| `--list-displays/-cameras/-camera-sizes/-encoders/-apps` | Run in the background to fill pickers. |
| `-h`, `-v`, `--pause-on-exit`, `--no-terminal-title` | Meaningless without a terminal. |
| `--window-title` | Always set to the device's friendly name, so window managers can match it. |
| `--print-fps` | Always on while a session runs; the app parses it for a live FPS readout. |
| `-V/--verbosity` | App setting ("Log level"), not per profile. |
| env `ADB`, `SCRCPY_SERVER_PATH`, `SCRCPY_ICON_DIR` | App settings → Paths. |

## 1. Session kind (the first, top-level choice)

A segmented control on the device page. It decides which sections exist at all.

| Kind | Emits | Sections shown |
|---|---|---|
| **Mirror** | `--video-source=display` (default) | Display, Video, Audio, Input, Device, Window, Record |
| **App window** | `--new-display[=WxH/dpi]` + usually `--start-app` | + Virtual display, App; Display picker hidden |
| **Camera** | `--video-source=camera` | Camera, Video, Audio, Window, Record; Input and Device hidden (no control in practice) |
| **Audio only** | `--no-video` | Audio, Record |
| **Keyboard & mouse (OTG)** | `--otg` | Input (aoa only); everything else hidden. USB only; works without adb authorization. |

Output toggles cut across kinds: **Show window** (default on; off = `--no-window`),
**Record**, **Webcam** (v4l2). "Nothing to do" (no window, no record, no v4l2) is
blocked at Start with the reason.

## 2. Sections

### Display (Mirror)
| Option | Tier | Control |
|---|---|---|
| `--display-id` | S | Picker filled from `--list-displays` (shows size per display); hidden if only one. |
| `--crop=w:h:x:y` | S | **Visual crop**: drag a rectangle over a live screenshot (`adb exec-out screencap -p`). Values stored in natural orientation. Disabled with flex display. |
| `--capture-orientation` | S | See Orientation. |
| `--display-ime-policy` | A | local / fallback / hide. Only for a secondary display (display-id ≠ 0, or App window). |

### Virtual display (App window)
| Option | Tier | Control |
|---|---|---|
| `--new-display=WxH/dpi` | Q | Size presets (Match device, 1920×1080, 1280×800 tablet, 2560×1440, custom) + DPI field (blank = device). |
| `-x/--flex-display` | Q | "Resize with window" toggle. Disables crop, window size, v4l2, read-only. Switches render fit default to unscaled. |
| `--start-app` | Q | Searchable app picker from `--list-apps` (label + package, system apps filtered). Options: "Force-stop first" (`+`). The `?` fuzzy prefix is unnecessary since we resolve the package ourselves. |
| `--no-vd-destroy-content` | A | "When closed: move apps to main screen / close apps". |
| `--no-vd-system-decorations` | A | "Show system bars" toggle. |

`--start-app` also works in Mirror mode; show it there under Device → "Launch app on start".

### Camera
| Option | Tier | Control |
|---|---|---|
| `--camera-id` / `--camera-facing` | Q | One picker: "Back (main)", "Front", "External", plus each id from `--list-cameras`. Emits id or facing, never both. |
| `--camera-size` / `--camera-ar` / `-m` | S | Radio: **Exact size** (list from `--list-camera-sizes` for the chosen camera) / **Aspect ratio** (sensor, 4:3, 16:9, custom) + max size. Exact size excludes the other two. |
| `--camera-fps` | S | Filled from the size list. |
| `--camera-high-speed` | S | Toggle; requires fps; narrows sizes to the high-speed list. |
| `--camera-zoom` | S | Slider. |
| `--camera-torch` | S | Toggle. |

Requires Android 12+: the Camera kind is disabled with that reason on older devices
(`getprop ro.build.version.sdk`).

### Video (quality and encoding)
| Option | Tier | Control |
|---|---|---|
| `-m/--max-size` | Q | Resolution presets: Native, 1920, 1600, 1280, 1024, 720, custom. Show the resulting WxH computed from the device size. |
| `-b/--video-bit-rate` | Q | Slider with stops (2M, 4M, 8M default, 16M, 32M, 64M) + numeric field with K/M units. |
| `--max-fps` | Q | 30 / 60 / 90 / 120 / Unlimited. Note Android 10+. |
| `--video-codec` | S | h264 / h265 / av1 / vp8 / vp9. Grey out codecs with no encoder in `--list-encoders`. |
| `--video-encoder` | S | Picker from `--list-encoders` filtered by codec; mark hardware vs software (`c2.android.*`/`OMX.google.*` are software). Default "Automatic". |
| `--video-buffer` | S | "Smoothing (adds latency)" ms slider, 0 default. |
| `--video-codec-options` | A | Key/type/value row editor (int, long, float, string) with common keys suggested (profile, level, i-frame-interval, bitrate-mode). |
| `--min-size-alignment` | A | 1 / 2 / 4 / 8 / 16. |
| `--ignore-video-encoder-constraints` | A | Toggle. |
| `--no-downsize-on-error` | A | "Lower resolution automatically on encoder error" (inverted, default on). |
| `--hwdec` | A | Auto / Off / VA-API (Linux) / D3D11VA (Win) / VideoToolbox (mac); only the platform's options. Off when v4l2 or no playback. |

### Audio
| Option | Tier | Control |
|---|---|---|
| `--no-audio` | Q | "Forward audio" toggle. |
| `--audio-source` | S | Grouped picker: **Device sound** (output: whole output, mutes phone; playback: apps that allow it, can keep playing on phone), **Microphone** (mic, unprocessed, camcorder, voice recognition, voice communication), **Calls** (voice-call, uplink, downlink), **Karaoke** (voice-performance). |
| `--audio-dup` | S | "Also play on phone"; only with playback. |
| `--no-audio-playback` | S | "Play on this computer" (off = record-only audio). |
| `--audio-codec` | S | opus / aac / flac / raw. |
| `--audio-bit-rate` | S | 64K / 128K / 192K / 256K; hidden for flac and raw. |
| `--audio-encoder` | A | From `--list-encoders`; hidden for raw. |
| `--audio-buffer` | A | ms slider, 50 default, "lower = less latency, more glitches". |
| `--audio-output-buffer` | A | ms, 10 default, flagged "rarely needed". |
| `--audio-codec-options` | A | Row editor; hidden for raw. |
| `--require-audio` | A | "Fail if audio doesn't start" (default: continue video-only). |

### Input
| Option | Tier | Control |
|---|---|---|
| `-n/--no-control` | Q | "Read-only" toggle; collapses the whole section and disables Device options that need control. |
| `--keyboard` | S | Cards: **Standard** (sdk), **Physical keyboard** (uhid: real layouts, needs one-time layout setup → button "Open keyboard settings on device"), **USB HID** (aoa, USB only), **Off**. `-K` never emitted, we write the long form. |
| `--mouse` | S | Same cards. uhid/aoa note: captures pointer, LAlt/LSuper releases it. sdk requires a window. |
| `--gamepad` | S | Off / Virtual (uhid) / USB (aoa). |
| `--mouse-bind` | S | **4×2 grid**: rows right, middle, button 4, button 5; columns "alone", "with Shift"; each cell a picker (Forward, Ignore, Back, Home, Recents, Notifications). The default shown follows the mouse mode (`bhsn:++++` sdk, `++++:bhsn` hid). |
| `--prefer-text` / `--raw-key-events` | S | One segmented "Typing": Auto / Text (better for accents and symbols) / Raw keys (better for games). sdk keyboard only. |
| `--no-key-repeat` | A | sdk keyboard only. |
| `--no-mouse-hover` | A | sdk mouse only. |
| `--no-clipboard-autosync` | S | "Sync clipboard" toggle. |
| `--legacy-paste` | A | "Paste by typing" (workaround). |
| `--shortcut-mod` | S | Chips for lctrl, rctrl, lalt, ralt, lsuper, rsuper (any of them triggers). Note: the help still mentions `+` combos but 5.0 rejects them, so offer single keys only. |
| `--push-target` | S | Path field, default `/sdcard/Download/` (drag-and-drop target). |

### Device (phone behaviour while connected)
All need control; disabled with "Requires input control" when read-only.

| Option | Tier | Control |
|---|---|---|
| `-S/--turn-screen-off` | Q | "Turn phone screen off". |
| `-w/--stay-awake` | Q | "Keep awake while plugged in". |
| `--keep-active` | S | "Keep screen on (simulate activity)". |
| `--screen-off-timeout` | S | Duration field. |
| `-t/--show-touches` | S | "Show physical touches". |
| `--no-power-on` | S | "Wake phone on start" (inverted). |
| `--power-off-on-close` | S | "Turn screen off when closing". |
| `--display-orientation` | S | See Orientation. |
| `--no-cleanup` | A | "Restore phone settings on exit" (inverted). |
| `--start-app` | S | (Mirror only; App window has it in Virtual display.) |

### Orientation (one combined widget used by Display/Device/Record)
`--capture-orientation`, `--display-orientation`, `--record-orientation`, `--orientation`,
`--angle`. A phone glyph rotated live, four rotation buttons, a flip toggle and a
"Lock rotation" toggle (`@`; "lock to current" = bare `@`). Expose capture and display
as tabs on one widget; record orientation lives in Record. Never emit `--orientation`
(it is shorthand). `--angle` is advanced (free degrees).

### Window
| Option | Tier | Control |
|---|---|---|
| `-f/--fullscreen` | Q | |
| `--always-on-top` | Q | |
| `--window-borderless` | S | |
| `--window-x/-y/-width/-height` | S | "Remember position" (app records last geometry per device) or explicit fields. Size disabled with flex display. |
| `--no-window-aspect-ratio-lock` | S | |
| `--render-fit` | S | Letterbox / Stretch / 1:1, small diagrams. Default follows flex display. |
| `--background-color` | S | Swatch picker. |
| `--disable-screensaver` | S | "Prevent computer sleep". |
| `--render-driver` | A | Platform-filtered list. |
| `--no-mipmaps` | A | "Smooth downscaling" (inverted). |

### Record
| Option | Tier | Control |
|---|---|---|
| `-r/--record` | Q | Toggle + folder + filename template (`{device}-{date}-{time}`), resolved at Start so each run gets a new file. |
| `--record-format` | S | Container from the extension. Audio-only kinds offer m4a / mka / opus / aac / flac / wav. Enforce codec match: aac→aac, flac→flac, opus→opus, wav→raw (offer one-click fix). |
| `--record-orientation` | S | 0/90/180/270. |
| `--time-limit` | S | Duration ("Stop after"). Applies to the session, so also usable without recording. |
| `--v4l2-sink` | S | "Webcam" output (Linux): pick from `/dev/video*` that are v4l2loopback; link to setup if none. Disables hwdec and flex display. |
| `--v4l2-buffer` | A | ms. |

### Connection (advanced, rarely touched)
| Option | Tier | Control |
|---|---|---|
| `--force-adb-forward` | A | |
| `--tunnel-host`, `--tunnel-port` | A | Imply forward; show that. For SSH-tunnelled remote adb. |
| `-p/--port` | A | Port or range. |
| `--kill-adb-on-close` | A | App setting rather than profile (the app shares the adb server). |

## 3. Rules the form enforces (from the binary)

Hide when not applicable, disable with a one-line reason when it conflicts within the
same kind. Each rule is data: `{when, then, reason}`.

- Camera options only with camera source; display-id, ime-policy, new-display only with display source.
- `--camera-id` xor `--camera-facing`; `--camera-size` excludes `--camera-ar` and `-m`; high-speed requires `--camera-fps`.
- `--display-id` xor `--new-display`; `--new-display` needs video.
- `--display-ime-policy` only on a secondary display.
- Flex display: only with new-display; excludes crop, read-only, v4l2, window width/height.
- Read-only excludes stay-awake, turn-screen-off, show-touches, power-off-on-close, keep-active, start-app, flex.
- `--prefer-text` xor `--raw-key-events`; both, and `--no-key-repeat`, only with sdk keyboard. `--no-mouse-hover` only with sdk mouse. sdk mouse needs video playback.
- `--audio-dup` only with playback source; bit rate, encoder, codec options ignored for raw (and bit rate for flac).
- Record format must match audio codec (aac, flac, opus, wav=raw). `--record-format` without `-r` is an error.
- hwdec needs playback and is incompatible with v4l2.
- OTG: no record, no read-only, not all inputs disabled; USB only.
- No video playback, no record, no v4l2 → video silently disabled: warn. Nothing to do at all → block.
- Camera needs Android 12+; `--max-fps` officially Android 10+.

## 4. Discovery calls (cached per device, refreshed on demand)

`--list-displays`, `--list-cameras`, `--list-camera-sizes`, `--list-encoders`,
`--list-apps`, plus `adb shell getprop` (model, sdk, abi), `wm size`, `wm density`,
`dumpsys battery`. Run them on first connect; show a spinner per picker, not globally.
Each `--list-*` starts the scrcpy server, so serialise them per device.
