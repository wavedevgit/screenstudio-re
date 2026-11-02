//go:build windows

package core

import (
	"io"
	"os"
	"os/exec"
)

func startScreenCapture(out string, _ *CursorSampler) (*exec.Cmd, func() error, <-chan error, error) {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "gdigrab", "-framerate", "60", "-draw_mouse", "1", "-i", "desktop",
		"-c:v", "libx264", "-preset", "superfast", "-threads", "2", "-crf", "16", out)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cmd, func() error {
		_, err := io.WriteString(stdin, "q\n")
		return err
	}, done, nil
}
