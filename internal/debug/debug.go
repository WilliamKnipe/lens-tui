// Package debug writes a local timing log so slow spots can be found.
// It is on by default; set LENS_DEBUG=0 to turn it off. Nothing leaves the
// machine: lines go to <user cache dir>/lens/debug.log (on macOS
// ~/Library/Caches/lens/debug.log), which is rotated at 5 MB.
package debug

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxSize = 5 << 20

var (
	mu  sync.Mutex
	out *os.File
	pid = os.Getpid()
)

// Init opens the log. It returns the log path, or "" when logging is off.
func Init() string {
	if v := os.Getenv("LENS_DEBUG"); v == "0" || v == "false" || v == "off" {
		return ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	dir = filepath.Join(dir, "lens")
	if os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	path := filepath.Join(dir, "debug.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ""
	}
	mu.Lock()
	out = f
	mu.Unlock()
	return path
}

// Path is where the log lives (whether or not it is enabled).
func Path() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "lens", "debug.log")
}

func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return out != nil
}

func Close() {
	mu.Lock()
	defer mu.Unlock()
	if out != nil {
		_ = out.Close()
		out = nil
	}
}

// Logf writes one line.
func Logf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if out == nil {
		return
	}
	fmt.Fprintf(out, "%s [%d] %s\n", time.Now().Format("15:04:05.000"), pid, fmt.Sprintf(format, args...))
}

// Since logs how long op took since start. Ops at or above slow are tagged
// SLOW; if onlySlow is set, faster ones are not logged at all (for things
// that run every second or every frame).
func Since(op string, start time.Time, slow time.Duration, onlySlow bool, detail string) time.Duration {
	d := time.Since(start)
	isSlow := d >= slow
	if onlySlow && !isSlow {
		return d
	}
	tag := ""
	if isSlow {
		tag = "  SLOW"
	}
	Logf("%-12s %8.1fms  %s%s", op, float64(d.Microseconds())/1000, detail, tag)
	return d
}
