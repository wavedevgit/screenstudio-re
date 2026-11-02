//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"

	"screenstudio-re/core"
)

const (
	previewW   = 800
	previewH   = 450
	previewFPS = 30
)

type previewFrame struct {
	generation uint64
	pixels     []byte
	position   float64
}

type videoPlayer struct {
	widget *gtk.Box
	image  *gtk.DrawingArea
	pixbuf *gdk.Pixbuf
	pixels []byte
	play   *gtk.Button
	scale  *gtk.Scale
	clock  *gtk.Label
	frames chan previewFrame
	buffers sync.Pool

	project    core.Project
	position   float64
	playing    bool
	updating   bool
	cmd        *exec.Cmd
	mu         sync.Mutex
	generation atomic.Uint64
	seekToken  atomic.Uint64
	seekTimer  *time.Timer
}

func newVideoPlayer() (*videoPlayer, error) {
	p := &videoPlayer{frames: make(chan previewFrame, 1)}
	p.buffers.New = func() any { return make([]byte, previewW*previewH*3) }
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 14)
	box.SetHAlign(gtk.ALIGN_CENTER)
	box.SetVAlign(gtk.ALIGN_CENTER)
	box.SetMarginStart(28)
	box.SetMarginEnd(28)
	box.SetMarginTop(24)
	box.SetMarginBottom(20)
	p.widget = box

	frame, _ := gtk.FrameNew("")
	frame.SetShadowType(gtk.SHADOW_NONE)
	addClass(frame, "preview-frame")
	// GtkImage snapshots a pixbuf on set; in-place pixel updates never repaint.
	// A draw callback reads pixbuf pixels fresh on every redraw instead.
	p.image, _ = gtk.DrawingAreaNew()
	p.image.SetSizeRequest(previewW, previewH)
	var err error
	p.pixbuf, err = gdk.PixbufNew(gdk.COLORSPACE_RGB, false, 8, previewW, previewH)
	if err != nil {
		return nil, err
	}
	p.pixels = p.pixbuf.GetPixels()
	p.image.Connect("draw", func(_ *gtk.DrawingArea, cr *cairo.Context) bool {
		cr.SetSourceRGB(0x0a/255.0, 0x0a/255.0, 0x0c/255.0)
		cr.Paint()
		gtk.GdkCairoSetSourcePixBuf(cr, p.pixbuf, 0, 0)
		cr.Paint()
		return true
	})
	frame.Add(p.image)
	box.PackStart(frame, true, true, 0)

	controls, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 10)
	controls.SetHAlign(gtk.ALIGN_FILL)
	p.play, _ = iconButton("media-playback-start-symbolic", "Play")
	p.play.Connect("clicked", p.toggle)
	controls.PackStart(p.play, false, false, 0)

	p.scale, _ = gtk.ScaleNewWithRange(gtk.ORIENTATION_HORIZONTAL, 0, 1, 0.01)
	p.scale.SetDrawValue(false)
	p.scale.SetHExpand(true)
	p.scale.Connect("value-changed", func() {
		if p.updating || p.project.VideoPath == "" {
			return
		}
		p.position = p.scale.GetValue()
		p.updateClock()
		if p.seekTimer != nil {
			p.seekTimer.Stop()
		}
		token := p.seekToken.Add(1)
		p.seekTimer = time.AfterFunc(120*time.Millisecond, func() {
			glib.IdleAdd(func() {
				if p.seekToken.Load() == token {
					p.startDecode(p.position, p.playing)
				}
			})
		})
	})
	controls.PackStart(p.scale, true, true, 0)

	p.clock, _ = gtk.LabelNew("0:00 / 0:00")
	addClass(p.clock, "timecode")
	controls.PackStart(p.clock, false, false, 0)
	box.PackStart(controls, false, false, 0)
	glib.TimeoutAdd(1000/previewFPS, func() bool {
		select {
		case frame := <-p.frames:
			if p.generation.Load() == frame.generation {
				copy(p.pixels, frame.pixels)
				p.image.QueueDraw()
				p.position = min(frame.position, p.project.Duration)
				p.updating = true
				p.scale.SetValue(p.position)
				p.updating = false
				p.updateClock()
			}
			p.buffers.Put(frame.pixels)
		default:
		}
		return true
	})
	return p, nil
}

func iconButton(icon, tooltip string) (*gtk.Button, error) {
	b, err := gtk.ButtonNew()
	if err != nil {
		return nil, err
	}
	img, _ := gtk.ImageNewFromIconName(icon, gtk.ICON_SIZE_BUTTON)
	b.SetImage(img)
	b.SetTooltipText(tooltip)
	addClass(b, "icon-button")
	return b, nil
}

func (p *videoPlayer) setProject(project core.Project, segments []core.Segment) {
	p.stop()
	p.project, p.position = project, 0
	p.scale.SetRange(0, max(project.Duration, 0.01))
	p.scale.ClearMarks()
	for _, s := range segments {
		if s.Zoom > 1 {
			p.scale.AddMark(s.Start, gtk.POS_BOTTOM, "")
			p.scale.AddMark(s.End, gtk.POS_BOTTOM, "")
		}
	}
	p.updating = true
	p.scale.SetValue(0)
	p.updating = false
	p.updateClock()
	p.startDecode(0, false)
}

func (p *videoPlayer) toggle() {
	if p.project.VideoPath == "" {
		return
	}
	if p.playing {
		p.stopDecode()
		p.setPlayIcon(false)
		return
	}
	if p.position >= p.project.Duration-0.05 {
		p.position = 0
	}
	p.startDecode(p.position, true)
}

func (p *videoPlayer) startDecode(from float64, playback bool) {
	gen := p.generation.Add(1)
	p.mu.Lock()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	p.mu.Unlock()

	args := []string{"-hide_banner", "-loglevel", "error", "-ss", fmt.Sprintf("%.3f", from)}
	if playback {
		args = append(args, "-re")
	}
	args = append(args, "-threads", "2", "-i", p.project.VideoPath, "-an", "-vf",
		fmt.Sprintf("fps=%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=0x0a0a0c", previewFPS, previewW, previewH, previewW, previewH),
		"-pix_fmt", "rgb24", "-f", "rawvideo")
	if !playback {
		args = append(args, "-frames:v", "1")
	}
	args = append(args, "-")
	cmd := exec.Command("ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return
	}
	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()
	p.playing = playback
	glib.IdleAdd(func() { p.setPlayIcon(playback) })
	duration := p.project.Duration

	go func() {
		frameSize := previewW * previewH * 3
		frameIndex := 0
		for {
			frame := p.buffers.Get().([]byte)[:frameSize]
			if _, err := io.ReadFull(stdout, frame); err != nil {
				p.buffers.Put(frame)
				break
			}
			if p.generation.Load() != gen {
				p.buffers.Put(frame)
				break
			}
			pos := from
			if playback {
				pos += float64(frameIndex) / previewFPS
			}
			if dropped := offerLatest(p.frames, previewFrame{generation: gen, pixels: frame, position: min(pos, duration)}); dropped != nil {
				p.buffers.Put(dropped)
			}
			frameIndex++
		}
		_ = cmd.Wait()
		p.mu.Lock()
		if p.cmd == cmd {
			p.cmd = nil
		}
		p.mu.Unlock()
		if p.generation.Load() == gen && playback {
			glib.IdleAdd(func() {
				p.playing = false
				p.setPlayIcon(false)
			})
		}
	}()
}

func offerLatest(frames chan previewFrame, frame previewFrame) []byte {
	select {
	case frames <- frame:
		return nil
	default:
	}
	var dropped []byte
	select {
	case stale := <-frames:
		dropped = stale.pixels
	default:
	}
	select {
	case frames <- frame:
	default:
		return frame.pixels
	}
	return dropped
}

func (p *videoPlayer) stopDecode() {
	p.generation.Add(1)
	p.mu.Lock()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	p.cmd = nil
	p.mu.Unlock()
	p.playing = false
}

func (p *videoPlayer) stop() {
	p.seekToken.Add(1)
	if p.seekTimer != nil {
		p.seekTimer.Stop()
	}
	p.stopDecode()
}

func (p *videoPlayer) setPlayIcon(playing bool) {
	name, tip := "media-playback-start-symbolic", "Play"
	if playing {
		name, tip = "media-playback-pause-symbolic", "Pause"
	}
	img, _ := gtk.ImageNewFromIconName(name, gtk.ICON_SIZE_BUTTON)
	p.play.SetImage(img)
	p.play.SetTooltipText(tip)
}

func (p *videoPlayer) updateClock() {
	p.clock.SetText(formatTime(p.position) + " / " + formatTime(p.project.Duration))
}

func formatTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds + 0.5)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}
