//go:build linux

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type CursorSampler struct {
	xconn   *xgb.Conn
	screen  *xproto.ScreenInfo
	quit    chan struct{}
	data    CursorData
	FPS     int
	t0      time.Time
	mu      sync.Mutex
	wg      sync.WaitGroup
	buttons uint16

	x, y       atomic.Int64
	wayland    bool
	kwin       *dbus.Conn
	scriptID   int32
	scriptName string
	scriptPath string
}

type cursorBridge struct{ c *CursorSampler }

// Update is called by the temporary KWin script on every cursor movement.
func (b *cursorBridge) Update(x, y int32) *dbus.Error {
	b.c.x.Store(int64(x))
	b.c.y.Store(int64(y))
	return nil
}

func NewCursorSampler() (*CursorSampler, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	return &CursorSampler{
		xconn:   conn,
		screen:  xproto.Setup(conn).DefaultScreen(conn),
		quit:    make(chan struct{}),
		FPS:     120,
		wayland: os.Getenv("WAYLAND_DISPLAY") != "",
	}, nil
}

func (c *CursorSampler) Start() error {
	c.data.Screen = [2]int{int(c.screen.WidthInPixels), int(c.screen.HeightInPixels)}
	c.data.FPS = c.FPS
	c.data.SourceCursorHidden = false
	c.t0 = time.Now()

	if c.wayland {
		if err := c.startKWinBridge(); err != nil {
			return fmt.Errorf("KWin cursor bridge: %w", err)
		}
	}
	q, err := xproto.QueryPointer(c.xconn, xproto.Window(c.screen.Root)).Reply()
	if err != nil && !c.wayland {
		return err
	}
	if err == nil {
		if !c.wayland {
			c.buttons = q.Mask
			c.x.Store(int64(q.RootX))
			c.y.Store(int64(q.RootY))
		}
	}

	c.data.Samples = [][3]float64{{0, float64(c.x.Load()), float64(c.y.Load())}}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		tick := time.NewTicker(time.Second / time.Duration(c.FPS))
		defer tick.Stop()
		for {
			select {
			case <-c.quit:
				return
			case <-tick.C:
				q, err := xproto.QueryPointer(c.xconn, xproto.Window(c.screen.Root)).Reply()
				if err == nil && !c.wayland {
					c.x.Store(int64(q.RootX))
					c.y.Store(int64(q.RootY))
					c.recordButtons(q.Mask)
				}
				p := [3]float64{time.Since(c.t0).Seconds(), float64(c.x.Load()), float64(c.y.Load())}
				c.mu.Lock()
				c.data.Samples = append(c.data.Samples, p)
				c.mu.Unlock()
			}
		}
	}()
	return nil
}

func (c *CursorSampler) Stop() CursorData {
	select {
	case <-c.quit:
	default:
		close(c.quit)
	}
	c.wg.Wait()
	c.stopKWinBridge()
	if c.xconn != nil {
		c.xconn.Close()
		c.xconn = nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data
}

func (c *CursorSampler) recordButtons(mask uint16) {
	t := time.Since(c.t0).Seconds()
	x, y := float64(c.x.Load()), float64(c.y.Load())
	for i, bit := range []uint16{xproto.KeyButMaskButton1, xproto.KeyButMaskButton2, xproto.KeyButMaskButton3} {
		wasDown, down := c.buttons&bit != 0, mask&bit != 0
		if wasDown == down {
			continue
		}
		type_ := MouseUp
		if down {
			type_ = MouseDown
		}
		c.mu.Lock()
		c.data.Events = append(c.data.Events, MouseEvent{T: t, X: x, Y: y, Button: i + 1, Type: type_})
		c.mu.Unlock()
	}
	c.buttons = mask
}

func (c *CursorSampler) ScreenSize() (int, int) {
	return int(c.screen.WidthInPixels), int(c.screen.HeightInPixels)
}

func (c *CursorSampler) startKWinBridge() error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	name := fmt.Sprintf("io.screenstudio.RE.Cursor%d", os.Getpid())
	if reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return fmt.Errorf("claim bus name: %v", err)
	}
	if err := conn.Export(&cursorBridge{c}, "/Cursor", "io.screenstudio.RE.Cursor"); err != nil {
		conn.Close()
		return err
	}
	scriptName := fmt.Sprintf("screenstudio_re_cursor_%d", os.Getpid())
	script := fmt.Sprintf(`
function reportCursor() {
    const p = workspace.cursorPos;
    callDBus(%q, "/Cursor", "io.screenstudio.RE.Cursor", "Update", p.x, p.y);
}
workspace.cursorPosChanged.connect(reportCursor);
reportCursor();
`, name)
	dir := filepath.Join(os.TempDir(), "screenstudio-re")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		conn.Close()
		return err
	}
	path := filepath.Join(dir, scriptName+".js")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		conn.Close()
		return err
	}

	var id int32
	scripting := conn.Object("org.kde.KWin", "/Scripting")
	if err := scripting.Call("org.kde.kwin.Scripting.loadScript", 0, path, scriptName).Store(&id); err != nil {
		os.Remove(path)
		conn.Close()
		return err
	}
	obj := conn.Object("org.kde.KWin", dbus.ObjectPath(fmt.Sprintf("/Scripting/Script%d", id)))
	if err := obj.Call("org.kde.kwin.Script.run", 0).Err; err != nil {
		_ = scripting.Call("org.kde.kwin.Scripting.unloadScript", 0, scriptName).Err
		os.Remove(path)
		conn.Close()
		return err
	}
	c.kwin, c.scriptID, c.scriptName, c.scriptPath = conn, id, scriptName, path
	// KWin reports the initial position asynchronously; give it one event turn.
	time.Sleep(25 * time.Millisecond)
	return nil
}

func (c *CursorSampler) stopKWinBridge() {
	if c.kwin == nil {
		return
	}
	obj := c.kwin.Object("org.kde.KWin", dbus.ObjectPath(fmt.Sprintf("/Scripting/Script%d", c.scriptID)))
	_ = obj.Call("org.kde.kwin.Script.stop", 0).Err
	var unloaded bool
	_ = c.kwin.Object("org.kde.KWin", "/Scripting").Call("org.kde.kwin.Scripting.unloadScript", 0, c.scriptName).Store(&unloaded)
	c.kwin.Close()
	os.Remove(c.scriptPath)
	c.kwin = nil
}
