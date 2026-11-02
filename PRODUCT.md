# Product

<!-- impeccable:product-schema 1 -->

## Platform

adaptive

## Users

People who record software demos, walkthroughs, tutorials, and product videos on KDE Linux or Windows and want the result framed and edited automatically.

## Product Purpose

Record a screen, pointer path, and clicks; derive click-focused zooms; preview the recording; and export a polished MP4 without a manual video-editing workflow.

## Positioning

The recording keeps the user's native cursor and stores pointer positions separately, then turns clicks or meaningful pointer stops into a spring-driven camera timeline.

## Operating Context

The first surface is a native recent-projects browser. A recording can be started from there. Opening a project provides native playback and scrubbing before export. Sessions are stored locally under `~/.screenstudio-re`.

## Capabilities and Constraints

- Native GTK3 interface on Linux/KDE and native Win32 controls on Windows; no browser or WebView.
- KDE Wayland capture uses the desktop portal, PipeWire, GStreamer, and a temporary KWin cursor bridge.
- X11 and Windows use ffmpeg capture paths.
- Export derives click zooms automatically and renders the framed 60 FPS MP4 in one ffmpeg pass without intermediate files.
- Project browser is the priority surface. Playback and scrubbing are required in this pass.
- Mic/system audio, camera, advanced trim editing, and full inspector controls remain undecided or unimplemented.

## Brand Commitments

The working product name is ScreenStudio RE. The workflow is informed by Screen Studio 4.1, but the interface should be an original polished native interpretation rather than a pixel-copy.

## Evidence on Hand

- Reverse-engineered Screen Studio 4.1 renderer and assets under `../deobf/` and `../asar/`.
- Existing recording, cursor sampling, automatic timeline, and export code under `core/`.
- User-provided screenshot of the rejected engineering-style interface.
- No fabricated customers, benchmarks, or commercial claims.

## Product Principles

- Recording must work reliably before visual polish matters.
- The project browser should make recent work recognizable at a glance.
- Playback and export state must remain obvious and recoverable.
- Platform-native behavior is more important than imitating macOS chrome.
- Automatic editing should reduce work without hiding the resulting timeline.
