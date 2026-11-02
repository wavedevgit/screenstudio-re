package core

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const renderFPS = 60

type ExportOpts struct {
	Canvas W `json:"canvas"`
}

type W struct{ W, H int }

type springValue struct{ value, velocity float64 }
type renderFrame struct{ t, cursorX, cursorY float64 }
type cursorPoint struct{ x, y float64 }
type trackPoint struct{ t, v float64 }

func ExportAuto(dir, out string, o ExportOpts) error {
	d, err := LoadCursor(filepath.Join(dir, "cursor.json"))
	if err != nil {
		return err
	}
	if o.Canvas.W <= 0 || o.Canvas.H <= 0 {
		o.Canvas = W{W: d.Screen[0], H: d.Screen[1]}
	}
	return Export(dir, out, d, BuildTimeline(d, DefaultTimelineOpts(2)).Segments, o)
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }

func springStep(s springValue, target, elapsedMs float64) springValue {
	const stiffness, damping, mass = 125.0, 12.0, 1.5
	d := damping
	delta := target - s.value
	if s.velocity*delta > 0 && math.Abs(s.velocity) > math.Abs(delta)*math.Sqrt(stiffness/mass) {
		d = max(d, 2*math.Sqrt(mass*stiffness))
	}
	steps := int(math.Ceil(elapsedMs))
	for i := 1; i <= steps; i++ {
		dt := .001
		if float64(i) > elapsedMs {
			dt = (float64(i) - elapsedMs) / 1000
		}
		s.velocity += (-(s.value-target)*stiffness - s.velocity*d) / mass * dt
		s.value += s.velocity * dt
		if math.Abs(s.velocity) < .002 && math.Abs(s.value-target) < .002 {
			return springValue{value: target}
		}
	}
	return s
}

func cursorFrames(d CursorData, contentW, contentH int) []renderFrame {
	end := d.Samples[len(d.Samples)-1][0]
	dt := 1.0 / renderFPS
	x := springValue{value: d.Samples[0][1]}
	y := springValue{value: d.Samples[0][2]}
	frames := make([]renderFrame, 0, int(end*renderFPS)+1)
	for i := 0; ; i++ {
		t := float64(i) * dt
		if t > end {
			break
		}
		rawX, rawY := posAt(d.Samples, t)
		x, y = springStep(x, rawX, dt*1000), springStep(y, rawY, dt*1000)
		frames = append(frames, renderFrame{
			t:       t,
			cursorX: x.value * float64(contentW) / float64(d.Screen[0]),
			cursorY: y.value * float64(contentH) / float64(d.Screen[1]),
		})
	}
	return frames
}

func even(v int) int {
	if v < 2 {
		return 2
	}
	return v - v%2
}

func contentRect(sw, sh int, canvas W) (x, y, w, h int) {
	maxW, maxH := even(int(float64(canvas.W)*0.9)), even(int(float64(canvas.H)*0.9))
	scale := min(float64(maxW)/float64(sw), float64(maxH)/float64(sh))
	w, h = even(int(float64(sw)*scale)), even(int(float64(sh)*scale))
	return (canvas.W - w) / 2, (canvas.H - h) / 2, w, h
}

func mouseDowns(events []MouseEvent) []MouseEvent {
	var clicks []MouseEvent
	for _, event := range events {
		if event.Type == MouseDown {
			clicks = append(clicks, event)
		}
	}
	sort.Slice(clicks, func(i, j int) bool { return clicks[i].T < clicks[j].T })
	return clicks
}

func commandScript(frames []renderFrame, d CursorData, contentW, contentH, ringSize int, hotspotX, hotspotY float64) string {
	var b strings.Builder
	halfFrame := .5 / renderFPS
	for _, frame := range frames {
		fmt.Fprintf(&b, "%s-%s [enter] overlay@cursor x %s,overlay@cursor y %s;",
			num(max(0, frame.t-halfFrame)), num(frame.t+halfFrame), num(frame.cursorX-hotspotX), num(frame.cursorY-hotspotY))
	}
	for _, event := range mouseDowns(d.Events) {
		x := event.X*float64(contentW)/float64(d.Screen[0]) - float64(ringSize)/2
		y := event.Y*float64(contentH)/float64(d.Screen[1]) - float64(ringSize)/2
		fmt.Fprintf(&b, "%s-%s [enter] overlay@ring x %s,overlay@ring y %s;",
			num(event.T), num(event.T+.001), num(x), num(y))
	}
	return b.String()
}

func segmentAt(segs []Segment, t float64) Segment {
	for _, segment := range segs {
		if t >= segment.Start && t < segment.End {
			return segment
		}
	}
	return Segment{Zoom: 1}
}

func focusAt(d CursorData, segs []Segment, t float64) (float64, float64) {
	segment := segmentAt(segs, t)
	if segment.Zoom <= 1 {
		return float64(d.Screen[0]) / 2, float64(d.Screen[1]) / 2
	}
	x, y := segment.X, segment.Y
	for _, event := range zoomEvents(d) {
		start := max(event.T-0.3, .001)
		if start >= segment.Start && start < segment.End && start <= t {
			x, y = event.X, event.Y
		}
	}
	return x, y
}

func cameraTracks(d CursorData, segs []Segment, canvas W, contentX, contentY, contentW, contentH int) (zoom, x, y []trackPoint) {
	end := d.Samples[len(d.Samples)-1][0]
	dt := 1.0 / renderFPS
	zs := springValue{value: 1}
	xs := springValue{value: float64(canvas.W) / 2}
	ys := springValue{value: float64(canvas.H) / 2}
	for i := 0; ; i++ {
		t := float64(i) * dt
		if t > end {
			break
		}
		segment := segmentAt(segs, t)
		focusX, focusY := focusAt(d, segs, t)
		targetX := float64(contentX) + focusX*float64(contentW)/float64(d.Screen[0])
		targetY := float64(contentY) + focusY*float64(contentH)/float64(d.Screen[1])
		zs = springStep(zs, max(segment.Zoom, 1), dt*1000)
		xs = springStep(xs, targetX, dt*1000)
		ys = springStep(ys, targetY, dt*1000)
		zoom = append(zoom, trackPoint{t, zs.value})
		x = append(x, trackPoint{t, xs.value})
		y = append(y, trackPoint{t, ys.value})
	}
	return simplifyTrack(zoom, .0005), simplifyTrack(x, .5), simplifyTrack(y, .5)
}

func simplifyTrack(points []trackPoint, tolerance float64) []trackPoint {
	if len(points) < 3 {
		return points
	}
	keep := make([]bool, len(points))
	keep[0], keep[len(points)-1] = true, true
	stack := [][2]int{{0, len(points) - 1}}
	for len(stack) > 0 {
		span := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		a, b := points[span[0]], points[span[1]]
		maxError, index := 0.0, -1
		for i := span[0] + 1; i < span[1]; i++ {
			f := (points[i].t - a.t) / (b.t - a.t)
			error := math.Abs(points[i].v - (a.v + (b.v-a.v)*f))
			if error > maxError {
				maxError, index = error, i
			}
		}
		if maxError > tolerance {
			keep[index] = true
			stack = append(stack, [2]int{span[0], index}, [2]int{index, span[1]})
		}
	}
	out := make([]trackPoint, 0, len(points))
	for i, point := range points {
		if keep[i] {
			out = append(out, point)
		}
	}
	return out
}

func linearExpr(points []trackPoint, variable string) string {
	if len(points) == 0 {
		return "0"
	}
	if len(points) == 1 {
		return num(points[0].v)
	}
	previousSlope := (points[1].v - points[0].v) / (points[1].t - points[0].t)
	terms := []string{num(points[0].v), num(previousSlope) + "*" + variable}
	for i := 2; i < len(points); i++ {
		slope := (points[i].v - points[i-1].v) / (points[i].t - points[i-1].t)
		delta := slope - previousSlope
		if math.Abs(delta) > 1e-9 {
			terms = append(terms, fmt.Sprintf("max(0,%s-%s)*%s", variable, num(points[i-1].t), num(delta)))
		}
		previousSlope = slope
	}
	for len(terms) > 1 {
		var next []string
		for i := 0; i < len(terms); i += 2 {
			if i+1 == len(terms) {
				next = append(next, terms[i])
			} else {
				next = append(next, "("+terms[i]+"+"+terms[i+1]+")")
			}
		}
		terms = next
	}
	return terms[0]
}

func polygonExpr(points []cursorPoint, scale float64) string {
	area := 0.0
	for i, p := range points {
		q := points[(i+1)%len(points)]
		area += p.x*q.y - q.x*p.y
	}
	parts := make([]string, 0, len(points))
	for i, p := range points {
		q := points[(i+1)%len(points)]
		op := "gte"
		if area > 0 {
			op = "lte"
		}
		parts = append(parts, fmt.Sprintf("%s((X-%s)*%s-(Y-%s)*%s,0)", op,
			num(p.x*scale), num((q.y-p.y)*scale), num(p.y*scale), num((q.x-p.x)*scale)))
	}
	return strings.Join(parts, "*")
}

func cursorSource(width, height int, duration float64) string {
	scale := float64(height) / 19
	outerArrow := polygonExpr([]cursorPoint{{.7, 16.4}, {.7, .4}, {12.3, 12}, {5.5, 12}, {5.1, 12.1}}, scale)
	outerStem := polygonExpr([]cursorPoint{{9.8, 17.1}, {6.2, 18.6}, {1.5, 7.5}, {5.2, 6}}, scale)
	innerArrow := polygonExpr([]cursorPoint{{1.7, 2.8}, {1.7, 14}, {4.7, 11.1}, {5.1, 11}, {9.9, 11}}, scale)
	innerStem := polygonExpr([]cursorPoint{{5.374, 9.019}, {3.53, 9.793}, {6.627, 17.17}, {8.471, 16.396}}, scale)
	outer := fmt.Sprintf("gt((%s)+(%s),0)", outerArrow, outerStem)
	inner := fmt.Sprintf("gt((%s)+(%s),0)", innerArrow, innerStem)
	return fmt.Sprintf("color=c=black@0:s=%dx%d:r=%d:d=%s,format=rgba,geq=r='if(%s,0,255)':g='if(%s,0,255)':b='if(%s,0,255)':a='if(%s,255,0)'[cursor]",
		width, height, renderFPS, num(duration), inner, inner, inner, outer)
}

func filterGraph(d CursorData, segs []Segment, canvas W, cursorImage bool) string {
	end := d.Samples[len(d.Samples)-1][0]
	contentX, contentY, contentW, contentH := contentRect(d.Screen[0], d.Screen[1], canvas)
	cursorH := max(32, int(math.Round(48*float64(canvas.H)/1080)))
	cursorW := max(22, int(math.Round(float64(cursorH)*13/19)))
	hotspotX, hotspotY := 0.0, 0.0
	if cursorImage && d.CursorSize[0] > 0 && d.CursorSize[1] > 0 {
		scale := float64(canvas.H) / float64(d.Screen[1])
		cursorW = max(1, int(math.Round(float64(d.CursorSize[0])*scale)))
		cursorH = max(1, int(math.Round(float64(d.CursorSize[1])*scale)))
		hotspotX, hotspotY = float64(d.CursorHotspot[0])*scale, float64(d.CursorHotspot[1])*scale
	}
	ringSize := cursorH * 2
	radius := max(8, int(math.Round(float64(canvas.H)*0.012)))
	corner := fmt.Sprintf("lte(pow(max(abs(X-W/2)-(W/2-%d),0),2)+pow(max(abs(Y-H/2)-(H/2-%d),0),2),%d)", radius, radius, radius*radius)

	var graph strings.Builder
	fmt.Fprintf(&graph, "[0:v]fps=%d,scale=%d:%d:flags=lanczos,setsar=1", renderFPS, contentW, contentH)
	if d.SourceCursorHidden {
		commands := commandScript(cursorFrames(d, contentW, contentH), d, contentW, contentH, ringSize, hotspotX, hotspotY)
		fmt.Fprintf(&graph, ",sendcmd=c='%s'", commands)
	}
	graph.WriteString("[screen];\n")
	current := "screen"
	if d.SourceCursorHidden {
		clicks := mouseDowns(d.Events)
		if len(clicks) > 0 {
			center := float64(ringSize-1) / 2
			ring := fmt.Sprintf("between(hypot(X-%s,Y-%s),%s,%s)", num(center), num(center), num(center*.55), num(center*.9))
			fmt.Fprintf(&graph, "color=c=white@0:s=%dx%d:r=%d:d=%s,format=rgba,geq=r=255:g=255:b=255:a='if(%s,115,0)'[ring];\n",
				ringSize, ringSize, renderFPS, num(end), ring)
			var enabled []string
			for _, click := range clicks {
				enabled = append(enabled, fmt.Sprintf("between(t,%s,%s)", num(click.T), num(click.T+.25)))
			}
			fmt.Fprintf(&graph, "[%s][ring]overlay@ring=x=-%d:y=-%d:eval=frame:enable='%s'[clicked];\n",
				current, ringSize, ringSize, strings.Join(enabled, "+"))
			current = "clicked"
		}
		if cursorImage {
			fmt.Fprintf(&graph, "[1:v]scale=%d:%d:flags=lanczos,format=rgba[cursor];\n", cursorW, cursorH)
		} else {
			graph.WriteString(cursorSource(cursorW, cursorH, end))
			graph.WriteString(";\n")
		}
		fmt.Fprintf(&graph, "color=c=black@0:s=%dx%d:r=%d:d=%s,format=rgba[clear];\n", contentW, contentH, renderFPS, num(end))
		graph.WriteString("[clear][cursor]overlay@cursor=x=0:y=0:eval=frame:format=auto[cursorLayer];\n")
		graph.WriteString("[cursorLayer]tmix=frames=4:weights='1 2 4 8',format=rgba[cursorBlur];\n")
		fmt.Fprintf(&graph, "[%s][cursorBlur]overlay=x=0:y=0:format=auto[cursored];\n", current)
		current = "cursored"
	}
	fmt.Fprintf(&graph, "color=white:s=%dx%d:r=1:d=%s,format=gray,geq=lum='if(%s,255,0)'[roundMask];\n",
		contentW, contentH, num(end), corner)
	fmt.Fprintf(&graph, "[%s]format=rgb24[screenRGB];[screenRGB][roundMask]alphamerge[foreground];\n", current)
	fmt.Fprintf(&graph, "color=c=black@0:s=%dx%d:r=1:d=%s,format=rgba,geq=r=0:g=0:b=0:a='if(%s,90,0)',boxblur=20:2[shadow];\n",
		contentW, contentH, num(end), corner)
	fmt.Fprintf(&graph, "gradients=s=%dx%d:r=%d:c0=3F37C9:c1=8C87DF:x0=0:y0=0:x1=%d:y1=%d:d=%s[background];\n",
		canvas.W, canvas.H, renderFPS, canvas.W, canvas.H, num(end))
	fmt.Fprintf(&graph, "[background][shadow]overlay=x=%d:y=%d:format=auto[withShadow];\n", contentX, contentY+max(4, canvas.H/120))
	fmt.Fprintf(&graph, "[withShadow][foreground]overlay=x=%d:y=%d:format=auto[framed];\n", contentX, contentY)

	zoomTrack, xTrack, yTrack := cameraTracks(d, segs, canvas, contentX, contentY, contentW, contentH)
	zoom := linearExpr(zoomTrack, "on/60")
	x := linearExpr(xTrack, "on/60")
	y := linearExpr(yTrack, "on/60")
	fmt.Fprintf(&graph, "[framed]zoompan=z='%s':x='clip((%s)-iw/zoom/2,0,iw-iw/zoom)':y='clip((%s)-ih/zoom/2,0,ih-ih/zoom)':d=1:s=%dx%d:fps=%d[camera];",
		zoom, x, y, canvas.W, canvas.H, renderFPS)
	graph.WriteString("[camera]format=yuv420p[out]")
	return graph.String()
}

func Export(dir, out string, d CursorData, segs []Segment, o ExportOpts) error {
	if len(segs) == 0 {
		return fmt.Errorf("empty timeline")
	}
	if o.Canvas.W <= 0 || o.Canvas.H <= 0 {
		return fmt.Errorf("invalid canvas")
	}
	end := d.Samples[len(d.Samples)-1][0]
	if end <= 0 || d.Screen[0] <= 0 || d.Screen[1] <= 0 {
		return fmt.Errorf("no cursor coverage")
	}
	cursorImage := false
	if stat, err := os.Stat(filepath.Join(dir, "pointer.png")); d.SourceCursorHidden && err == nil && stat.Size() > 0 {
		cursorImage = true
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", filepath.Join(dir, "recording.mp4")}
	if cursorImage {
		args = append(args, "-loop", "1", "-i", filepath.Join(dir, "pointer.png"))
	}
	args = append(args, "-filter_complex_threads", "1", "-filter_complex_script", "pipe:0",
		"-map", "[out]", "-t", num(end), "-r", strconv.Itoa(renderFPS),
		"-c:v", "libx264", "-preset", "veryfast", "-threads", "2", "-crf", "15",
		"-movflags", "+faststart", out)
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdin = strings.NewReader(filterGraph(d, segs, o.Canvas, cursorImage))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	for _, pattern := range []string{"seg*.mp4", "concat.txt", "cursor.png"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, match := range matches {
			_ = os.Remove(match)
		}
	}
	return nil
}
