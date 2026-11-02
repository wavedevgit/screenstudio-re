# ScreenStudio RE

A native screen recorder for KDE Wayland, X11, and Windows. One button records the screen with the pointer and clicks, then exports an edited MP4 with automatic zoom moves on clicks — no timeline editing.

## Run

```sh
cd app
go run .
```

## Sessions

Recordings live in `~/.screenstudio-re/<timestamp>/` as `recording.mp4`, `cursor.json`, and `output.mp4`.
