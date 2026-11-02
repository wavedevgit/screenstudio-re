package main

import (
	"strconv"
	"strings"

	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	root := flag.String("root", defaultRoot(), "session store root")
	flag.Parse()

	if err := os.MkdirAll(*root, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := runGUI(*root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "screenstudio-re")
	}
	return filepath.Join(home, ".screenstudio-re")
}

// openFile shows a path in the system default handler.
func openFile(path string) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/c", "start", "", path)
	} else if p, err := exec.LookPath("xdg-open"); err == nil {
		c = exec.Command(p, path)
	}
	if c != nil {
		c.Start()
	}
}

func parseWxH(s string) (int, int) {
	a, b, ok := strings.Cut(s, "x")
	if !ok {
		return 1920, 1080
	}
	w, errA := strconv.Atoi(a)
	h, errB := strconv.Atoi(b)
	if errA != nil || errB != nil || w <= 0 || h <= 0 {
		return 1920, 1080
	}
	return w, h
}
