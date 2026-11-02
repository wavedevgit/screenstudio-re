//go:build linux

package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

var portalToken atomic.Uint64

type portalStream struct {
	Node  uint32
	Props map[string]dbus.Variant
}

func startScreenCapture(out string, sampler *CursorSampler) (*exec.Cmd, func() error, <-chan error, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return startWaylandCapture(out)
	}
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":0"
	}
	w, h := sampler.ScreenSize()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "x11grab", "-video_size", strconv.Itoa(w)+"x"+strconv.Itoa(h),
		"-framerate", "60", "-draw_mouse", "1", "-i", display,
		"-c:v", "libx264", "-preset", "superfast", "-threads", "2", "-crf", "16", out)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return cmd, func() error { return cmd.Process.Signal(os.Interrupt) }, done, nil
}

// startWaylandCapture asks the desktop portal for a monitor stream, then hands
// its PipeWire fd/node directly to GStreamer. KDE shows its native chooser.
func startWaylandCapture(out string) (*exec.Cmd, func() error, <-chan error, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	desktop := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	sessionToken := token("session")
	results, err := portalRequest(ctx, conn, desktop, "org.freedesktop.portal.ScreenCast.CreateSession", map[string]dbus.Variant{
		"handle_token":         dbus.MakeVariant(token("request")),
		"session_handle_token": dbus.MakeVariant(sessionToken),
	})
	if err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("create portal session: %w", err)
	}
	value := results["session_handle"].Value()
	session, ok := value.(dbus.ObjectPath)
	if !ok {
		if path, isString := value.(string); isString {
			session, ok = dbus.ObjectPath(path), true
		}
	}
	if !ok || !session.IsValid() {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("portal returned no session (%T: %v)", value, value)
	}

	_, err = portalRequest(ctx, conn, desktop, "org.freedesktop.portal.ScreenCast.SelectSources", session, map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token("select")),
		"types":        dbus.MakeVariant(uint32(1)), // monitor
		"multiple":     dbus.MakeVariant(false),
		"cursor_mode":  dbus.MakeVariant(uint32(2)), // embed KDE's real cursor in the captured frames
	})
	if err != nil {
		closePortal(conn, session)
		return nil, nil, nil, fmt.Errorf("select screen: %w", err)
	}
	results, err = portalRequest(ctx, conn, desktop, "org.freedesktop.portal.ScreenCast.Start", session, "", map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token("start")),
	})
	if err != nil {
		closePortal(conn, session)
		return nil, nil, nil, fmt.Errorf("start screen: %w", err)
	}
	var streams []portalStream
	if v, ok := results["streams"]; !ok || dbus.Store([]interface{}{v.Value()}, &streams) != nil || len(streams) == 0 {
		closePortal(conn, session)
		return nil, nil, nil, fmt.Errorf("portal returned no PipeWire stream (%T)", results["streams"].Value())
	}

	var remote dbus.UnixFD
	if err := desktop.CallWithContext(ctx, "org.freedesktop.portal.ScreenCast.OpenPipeWireRemote", 0, session, map[string]dbus.Variant{}).Store(&remote); err != nil {
		closePortal(conn, session)
		return nil, nil, nil, fmt.Errorf("open PipeWire: %w", err)
	}
	pipewire := os.NewFile(uintptr(remote), "pipewire-remote")
	if pipewire == nil {
		closePortal(conn, session)
		return nil, nil, nil, fmt.Errorf("invalid PipeWire fd")
	}

	// -e sends EOS on SIGINT, so mp4mux writes its final index before exit.
	cmd := exec.Command("gst-launch-1.0", "-e", "-q",
		"pipewiresrc", "fd=3", "path="+strconv.FormatUint(uint64(streams[0].Node), 10), "do-timestamp=true", "!",
		"videoconvert", "!", "videorate", "!", "video/x-raw,framerate=60/1", "!",
		"x264enc", "speed-preset=superfast", "threads=2", "tune=zerolatency", "bitrate=20000", "!",
		"h264parse", "!", "mp4mux", "faststart=true", "!", "filesink", "location="+out)
	cmd.ExtraFiles = []*os.File{pipewire}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		pipewire.Close()
		closePortal(conn, session)
		return nil, nil, nil, fmt.Errorf("gstreamer: %w", err)
	}
	pipewire.Close()
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		closePortal(conn, session)
		done <- err
	}()
	return cmd, func() error { return cmd.Process.Signal(syscall.SIGINT) }, done, nil
}

func token(prefix string) string {
	return fmt.Sprintf("screenstudio_%s_%d", prefix, portalToken.Add(1))
}

func portalRequest(ctx context.Context, conn *dbus.Conn, obj dbus.BusObject, method string, args ...any) (map[string]dbus.Variant, error) {
	signals := make(chan *dbus.Signal, 1)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return nil, err
	}
	defer conn.RemoveMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	)
	var request dbus.ObjectPath
	if err := obj.CallWithContext(ctx, method, 0, args...).Store(&request); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case sig := <-signals:
			if sig.Path != request {
				continue
			}
			if len(sig.Body) != 2 {
				return nil, fmt.Errorf("malformed portal response")
			}
			code, _ := sig.Body[0].(uint32)
			if code != 0 {
				return nil, fmt.Errorf("request canceled (%d)", code)
			}
			values, ok := sig.Body[1].(map[string]dbus.Variant)
			if !ok {
				return nil, fmt.Errorf("malformed portal results")
			}
			return values, nil
		}
	}
}

func closePortal(conn *dbus.Conn, path dbus.ObjectPath) {
	if conn == nil {
		return
	}
	_ = conn.Object("org.freedesktop.portal.Desktop", path).Call("org.freedesktop.portal.Session.Close", 0).Err
	conn.Close()
}
