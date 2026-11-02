# ScreenStudio RE — Go rebuild

An open reimplementation of Screen Studio's core idea, in Go:

1. **Record** the screen with the user's native cursor while separately sampling its position for automatic framing: KDE Wayland uses the desktop portal + PipeWire/GStreamer + a temporary KWin position bridge; X11/Windows use ffmpeg.
2. **Auto-edit**: stopping immediately creates Screen Studio-style 2× ranges around recorded clicks and renders `output.mp4`.
3. **Export**: one 60 FPS ffmpeg pass applies the recovered spring, camera motion blur, gradient background, inset, rounded corners, and shadow—without temporary render files.

The UI uses actual platform widgets: GTK3 on Linux/KDE and Win32 controls on Windows (walk). There is no browser, local HTTP server, WebView, HTML, CSS, or JavaScript.

## Run

```sh
cd app
go run .            # opens the native GTK3 window
```

- **● button** starts/stops a recording (screen + pointer), then automatically renders the zoomed video.
- Pick a session → timeline preview (red bars = zooms) → **Export MP4** (canvas selectable).
- Sessions live in `~/.screenstudio-re/<timestamp>/`: `recording.mp4`, `cursor.json`, `output.mp4`.

Verified on KDE Wayland: portal capture produced a non-black 1920×1080 H.264 recording; KWin supplied live cursor positions; automatic timeline export produced a synchronized 1280×720 MP4. Unit/e2e tests are in `go test ./core/`.

## Layout

- `core/timeline.go` — recorded clicks → recovered automatic zoom ranges.
- `core/cursor_sampler_{linux,windows}.go` — X11/Win32 pointer sampling; KDE Wayland cursor bridge.
- `core/capture_{linux,windows}.go` — portal/PipeWire/GStreamer on Wayland, ffmpeg elsewhere.
- `core/record.go` — capture orchestration and session persistence.
- `core/render.go` — the one-pass compositor and offline `{125,12,1.5,clamp:true,precision:0.002}` spring simulation.
- `gui_gtk.go` — native GTK3 Linux GUI.
- `gui_windows.go` — native Win32 GUI.
- `main.go` — platform-independent entry point.

## Known ceilings (intentional v1)

- KDE Wayland does not expose global click events to regular apps, so automatic framing falls back to meaningful pointer stops there.
- KDE Wayland and X11 are implemented; other Wayland compositors are not yet.
- Multi-monitor coordinate normalization, mic/system audio, camera, and device presets: not yet.
