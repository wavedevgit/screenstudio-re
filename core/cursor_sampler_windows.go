//go:build windows

package core

import (
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procGetCursorPos = user32.NewProc("GetCursorPos")
	procGetMetrics   = user32.NewProc("GetSystemMetrics")
	procGetKeyState  = user32.NewProc("GetAsyncKeyState")
)

type point struct{ X, Y int32 }

func cursorPos() (int, int) {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

type CursorSampler struct {
	quit    chan struct{}
	data    CursorData
	FPS     int
	t0      time.Time
	mu      sync.Mutex
	wg      sync.WaitGroup
	buttons uint8
}

func NewCursorSampler() (*CursorSampler, error) {
	return &CursorSampler{quit: make(chan struct{}), FPS: 120}, nil
}

func (c *CursorSampler) Start() error {
	w, _, _ := procGetMetrics.Call(0) // SM_CXSCREEN
	h, _, _ := procGetMetrics.Call(1) // SM_CYSCREEN
	c.data.Screen = [2]int{int(w), int(h)}
	c.data.FPS = c.FPS
	c.data.SourceCursorHidden = false
	x, y := cursorPos()
	c.t0 = time.Now()
	c.buttons, _ = winButtons()
	c.data.Samples = [][3]float64{{0, float64(x), float64(y)}}
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
				x, y := cursorPos()
				t := time.Since(c.t0).Seconds()
				buttons, pressed := winButtons()
				c.mu.Lock()
				c.data.Samples = append(c.data.Samples, [3]float64{t, float64(x), float64(y)})
				for i := 0; i < 3; i++ {
					bit := uint8(1 << i)
					wasDown, down, wasPressed := c.buttons&bit != 0, buttons&bit != 0, pressed&bit != 0
					if !wasDown && (down || wasPressed) {
						c.data.Events = append(c.data.Events, MouseEvent{T: t, X: float64(x), Y: float64(y), Button: i + 1, Type: MouseDown})
					}
					if (wasDown && !down) || (!wasDown && !down && wasPressed) {
						c.data.Events = append(c.data.Events, MouseEvent{T: t, X: float64(x), Y: float64(y), Button: i + 1, Type: MouseUp})
					}
				}
				c.buttons = buttons
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data
}

func winButtons() (down, pressed uint8) {
	for i, key := range []uintptr{1, 4, 2} { // left, middle, right
		state, _, _ := procGetKeyState.Call(key)
		bit := uint8(1 << i)
		if state&0x8000 != 0 {
			down |= bit
		}
		if state&1 != 0 {
			pressed |= bit
		}
	}
	return down, pressed
}

func (c *CursorSampler) ScreenSize() (int, int) {
	x, y := c.data.Screen[0], c.data.Screen[1]
	return x, y
}
