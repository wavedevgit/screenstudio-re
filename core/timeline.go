package core

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// Sample = [t, x, y]: seconds, screen-space pointer coords.
type CursorData struct {
	Screen             [2]int       `json:"screen"`
	FPS                int          `json:"fps"`
	Samples            [][3]float64 `json:"samples"`
	Events             []MouseEvent `json:"events,omitempty"`
	SourceCursorHidden bool         `json:"sourceCursorHidden,omitempty"`
	CursorSize         [2]int       `json:"cursorSize,omitempty"`
	CursorHotspot      [2]int       `json:"cursorHotspot,omitempty"`
}

type MouseEventType string

const (
	MouseDown MouseEventType = "mouseDown"
	MouseUp   MouseEventType = "mouseUp"
)

type MouseEvent struct {
	T      float64        `json:"t"`
	X      float64        `json:"x"`
	Y      float64        `json:"y"`
	Button int            `json:"button"`
	Type   MouseEventType `json:"type"`
}

type Segment struct {
	Start, End float64
	Zoom       float64
	X, Y       float64 // focal point, screen px
}

type Timeline struct {
	Segments []Segment
	Screen   [2]int
}

type TimelineOpts struct {
	ZoomLevel float64
}

func DefaultTimelineOpts(zoom float64) TimelineOpts {
	if zoom <= 0 {
		zoom = 2
	}
	return TimelineOpts{ZoomLevel: zoom}
}

// slideSmooth is an O(n) sliding-window moving average over the path.
func slideSmooth(s [][3]float64, w float64) [][3]float64 {
	if len(s) < 2 || w <= 0 {
		return s
	}
	out := make([][3]float64, len(s))
	lo := 0
	sx, sy := 0.0, 0.0
	for i, p := range s {
		sx += p[1]
		sy += p[2]
		for p[0]-s[lo][0] > w && lo < i {
			sx -= s[lo][1]
			sy -= s[lo][2]
			lo++
		}
		n := float64(i - lo + 1)
		out[i] = [3]float64{p[0], sx / n, sy / n}
	}
	return out
}

func posAt(s [][3]float64, t float64) (float64, float64) {
	i := sort.Search(len(s), func(i int) bool { return s[i][0] >= t })
	if i == 0 {
		return s[0][1], s[0][2]
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	a, b := s[i-1], s[i]
	if b[0] == a[0] {
		return b[1], b[2]
	}
	f := (t - a[0]) / (b[0] - a[0])
	return a[1] + (b[1]-a[1])*f, a[2] + (b[2]-a[2])*f
}

func BuildTimeline(d CursorData, o TimelineOpts) Timeline {
	end := d.Samples[len(d.Samples)-1][0]
	events := zoomEvents(d)
	var zooms []Segment
	for _, event := range events {
		if event.Type != MouseDown || event.T >= end-1 {
			continue
		}
		start := max(event.T-0.3, 0.001)
		stop := min(event.T+2.5, end-0.8)
		if stop <= start {
			continue
		}
		zoom := Segment{Start: start, End: stop, Zoom: o.ZoomLevel, X: event.X, Y: event.Y}
		if len(zooms) > 0 && start-zooms[len(zooms)-1].End <= 2.5 {
			last := &zooms[len(zooms)-1]
			last.End = max(last.End, stop)
			continue
		}
		zooms = append(zooms, zoom)
	}
	var segments []Segment
	cursor := 0.0
	for _, zoom := range zooms {
		if cursor < zoom.Start {
			segments = append(segments, Segment{Start: cursor, End: zoom.Start, Zoom: 1})
		}
		segments = append(segments, zoom)
		cursor = zoom.End
	}
	if cursor < end || len(segments) == 0 {
		segments = append(segments, Segment{Start: cursor, End: end, Zoom: 1})
	}
	return Timeline{Segments: segments, Screen: d.Screen}
}

func zoomEvents(d CursorData) []MouseEvent {
	var clicks []MouseEvent
	for _, event := range d.Events {
		if event.Type == MouseDown {
			clicks = append(clicks, event)
		}
	}
	if len(clicks) > 0 {
		sort.Slice(clicks, func(i, j int) bool { return clicks[i].T < clicks[j].T })
		return clicks
	}

	var stops []MouseEvent
	moving, distance := false, 0.0
	lastMoveT, lastMoveX, lastMoveY, lastFocusT := 0.0, 0.0, 0.0, -10.0
	for i := 1; i < len(d.Samples); i++ {
		previous, point := d.Samples[i-1], d.Samples[i]
		step := math.Hypot(point[1]-previous[1], point[2]-previous[2])
		if step >= 1.5 {
			distance += step
			moving = moving || distance >= 40
			lastMoveT, lastMoveX, lastMoveY = point[0], point[1], point[2]
			continue
		}
		if moving && point[0]-lastMoveT >= .18 {
			if lastMoveT-lastFocusT >= 1 {
				stops = append(stops, MouseEvent{T: lastMoveT, X: lastMoveX, Y: lastMoveY, Button: 1, Type: MouseDown})
				lastFocusT = lastMoveT
			}
			moving, distance = false, 0
		}
	}
	return stops
}

func LoadCursor(path string) (CursorData, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return CursorData{}, err
	}
	var d CursorData
	if err := json.Unmarshal(b, &d); err != nil {
		return CursorData{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if len(d.Samples) < 2 {
		return d, fmt.Errorf("no cursor data")
	}
	return d, nil
}
