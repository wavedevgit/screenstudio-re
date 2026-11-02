package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Session struct {
	Name    string    `json:"name"`
	W       int       `json:"w"`
	H       int       `json:"h"`
	Started time.Time `json:"started"`
}

type Recorder struct {
	sampler *CursorSampler
	cmd     *exec.Cmd
	stop    func() error
	done    <-chan error
	cutPath string
	dirPath string
}

// Record kicks off: ffmpeg screen capture + cursor sampler, both stopped by Stop.
func (r *Recorder) Record(root string) (*Session, error) {
	name := time.Now().Format("2006-01-02_15-04-05")
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if r.sampler == nil {
		s, err := NewCursorSampler()
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("cursor: %w", err)
		}
		r.sampler = s
	}
	w, h := r.sampler.ScreenSize()
	cut := filepath.Join(dir, "recording.mp4")

	cmd, stop, done, err := startScreenCapture(cut, r.sampler)
	if err != nil {
		r.sampler.Stop()
		r.sampler = nil
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("screen capture: %w", err)
	}
	if err := r.sampler.Start(); err != nil {
		_ = stop()
		<-done
		r.sampler.Stop()
		r.sampler = nil
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("cursor: %w", err)
	}
	select {
	case err := <-done:
		r.sampler.Stop()
		r.sampler = nil
		_ = os.RemoveAll(dir)
		if err == nil {
			err = fmt.Errorf("capture exited unexpectedly")
		}
		return nil, fmt.Errorf("screen capture: %w", err)
	case <-time.After(250 * time.Millisecond):
	}
	r.cmd = cmd
	r.stop = stop
	r.done = done
	r.cutPath = cut
	r.dirPath = dir
	return &Session{Name: name, W: w, H: h, Started: time.Now()}, nil
}

// Stop closes both capture legs, flushes cursor.json.
func (r *Recorder) Stop() (string, error) {
	if r.cmd == nil {
		return "", fmt.Errorf("not running")
	}
	data := r.sampler.Stop()
	var captureErr error
	endedEarly := false
	select {
	case captureErr = <-r.done:
		endedEarly = true
	default:
	}
	timedOut := false
	if !endedEarly {
		if r.stop != nil {
			_ = r.stop()
		}
		select {
		case captureErr = <-r.done:
		case <-time.After(10 * time.Second):
			timedOut = true
			_ = r.cmd.Process.Kill()
			captureErr = <-r.done
		}
	}
	stat, statErr := os.Stat(r.cutPath)
	if endedEarly || timedOut || statErr != nil || stat.Size() == 0 {
		dir := r.dirPath
		r.reset()
		_ = os.RemoveAll(dir)
		if endedEarly {
			if captureErr != nil {
				return "", fmt.Errorf("screen capture stopped early: %w", captureErr)
			}
			return "", fmt.Errorf("screen capture stopped early")
		}
		if timedOut {
			return "", fmt.Errorf("screen capture did not finish cleanly")
		}
		if captureErr != nil {
			return "", fmt.Errorf("screen capture failed: %w", captureErr)
		}
		return "", fmt.Errorf("screen capture produced no video")
	}
	b, err := json.MarshalIndent(data, "", " ")
	if err != nil {
		return r.cutPath, err
	}
	cp := filepath.Join(r.dirPath, "cursor.json")
	if err := os.WriteFile(cp, b, 0o644); err != nil {
		return r.cutPath, err
	}
	out := r.cutPath
	r.reset()
	return out, nil
}

func (r *Recorder) reset() {
	r.sampler, r.cmd, r.stop, r.done, r.cutPath, r.dirPath = nil, nil, nil, nil, "", ""
}

func Sessions(root string) ([]string, error) {
	ms, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range ms {
		if m.IsDir() {
			out = append(out, m.Name())
		}
	}
	return out, nil
}
