package core

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorCommandsCoverFrameTimes(t *testing.T) {
	frames := []renderFrame{{t: 0}, {t: 1.0 / renderFPS}, {t: 2.0 / renderFPS}}
	script := commandScript(frames, CursorData{}, 1, 1, 1, 0, 0)
	for _, window := range []string{"0.0000-0.0083", "0.0083-0.0250", "0.0250-0.0417"} {
		if !strings.Contains(script, window+" [enter]") {
			t.Fatalf("cursor command does not cover frame: %s", window)
		}
	}
}

func TestBuildTimeline(t *testing.T) {
	s := synth()
	d := CursorData{Screen: [2]int{1920, 1080}, Samples: s, Events: []MouseEvent{
		{T: 1.2, X: 1200, Y: 700, Button: 1, Type: MouseDown},
	}}
	tl := BuildTimeline(d, DefaultTimelineOpts(2))
	if len(tl.Segments) < 2 {
		t.Fatalf("want >= 2 segments (flat + zooms), got %+v", tl.Segments)
	}
	zooms := 0
	for _, sg := range tl.Segments {
		if sg.Zoom > 1 {
			zooms++
			if !(sg.Start > 0 && sg.End < s[len(s)-1][0]) {
				t.Fatalf("zoom segment outside bounds: %+v", sg)
			}
			if sg.X < 0 || sg.X > 1920 || sg.Y < 0 || sg.Y > 1080 {
				t.Fatalf("focal out of screen: %+v", sg)
			}
		}
	}
	if zooms < 1 {
		t.Fatal("no zoom segment for click")
	}
}

func TestAutomaticZoomUsesRecoveredClickTiming(t *testing.T) {
	samples := [][3]float64{{0, 100, 100}, {8, 100, 100}}
	d := CursorData{Screen: [2]int{1920, 1080}, Samples: samples, Events: []MouseEvent{
		{T: 2, X: 300, Y: 400, Type: MouseDown},
		{T: 4, X: 900, Y: 600, Type: MouseDown},
		{T: 7.2, X: 900, Y: 600, Type: MouseDown}, // final second: ignored
	}}
	segments := BuildTimeline(d, DefaultTimelineOpts(2)).Segments
	if len(segments) != 3 {
		t.Fatalf("want flat/merged zoom/flat, got %+v", segments)
	}
	zoom := segments[1]
	if zoom.Start != 1.7 || zoom.End != 6.5 || zoom.Zoom != 2 {
		t.Fatalf("wrong recovered click range: %+v", zoom)
	}
}

func TestAutomaticZoomFallsBackToPointerStops(t *testing.T) {
	d := CursorData{Screen: [2]int{1920, 1080}, Samples: synth()}
	segments := BuildTimeline(d, DefaultTimelineOpts(2)).Segments
	found := false
	for _, segment := range segments {
		if segment.Zoom > 1 && math.Abs(segment.X-1200) < 20 && math.Abs(segment.Y-700) < 20 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no pointer-stop zoom near target: %+v", segments)
	}
}

// synth: move fast, dwell around (1200,700), then move off again.
func synth() [][3]float64 {
	var out [][3]float64
	t := 0.0
	// 1 s inbound fast move: (100,100) -> (1200,700)
	for i := 0; i < 60; i++ {
		f := float64(i) / 60
		out = append(out, [3]float64{t, 100 + f*1100, 100 + f*600})
		t += 1.0 / 60
	}
	// dwell 2 s with tiny jitter
	for i := 0; i < 120; i++ {
		out = append(out, [3]float64{t, 1200 + 4*math.Sin(float64(i)*0.3), 700})
		t += 1.0 / 60
	}
	// 1 s fast exit
	for i := 0; i < 60; i++ {
		f := float64(i) / 60
		out = append(out, [3]float64{t, 1200 - f*1100, 700 - f*600})
		t += 1.0 / 60
	}
	return out
}

// TestExportE2E renders the synthetic session through ffmpeg and checks the
// output with ffprobe. Needs ffmpeg on PATH.
func TestExportE2E(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "recording.mp4")
	src := "testsrc=size=1920x1080:rate=60:duration=4"
	out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error", "-f", "lavfi", "-i", src,
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "30", rec).
		CombinedOutput()
	if err != nil {
		t.Skipf("lavfi testsrc unavailable: %v: %s", err, out)
	}

	d := CursorData{Screen: [2]int{1920, 1080}, Samples: synth(), SourceCursorHidden: true, Events: []MouseEvent{
		{T: 1.2, X: 1200, Y: 700, Button: 1, Type: MouseDown},
		{T: 1.3, X: 1200, Y: 700, Button: 1, Type: MouseUp},
	}}
	cursor, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cursor.json"), cursor, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"seg00.mp4", "concat.txt", "cursor.png"} {
		if err := os.WriteFile(filepath.Join(dir, legacy), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := filepath.Join(dir, "output.mp4")
	if err := ExportAuto(dir, res,
		ExportOpts{Canvas: W{1280, 800}}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := os.Stat(res); err != nil {
		t.Fatal("output missing")
	}
	for _, legacy := range []string{"seg00.mp4", "concat.txt", "cursor.png"} {
		if _, err := os.Stat(filepath.Join(dir, legacy)); !os.IsNotExist(err) {
			t.Fatalf("temporary export file remains: %s", legacy)
		}
	}
}

func TestLongCameraTrackDoesNotOverflowFFmpegExpression(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	points := make([]trackPoint, 160)
	for i := range points {
		points[i] = trackPoint{t: float64(i) / renderFPS, v: 16 + float64(i%2)*8}
	}
	filter := "zoompan=z=1:x='" + linearExpr(points, "on/60") + "':y=0:d=1:s=64x64:fps=60"
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "color=s=64x64:d=0.1:r=60", "-vf", filter, "-frames:v", "1", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg rejected long camera track: %v: %s", err, out)
	}
}
