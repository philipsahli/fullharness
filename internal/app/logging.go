package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// logCap keeps fh.log to one file of at most 10 MB (#4): when a write would
// pass it, the file starts over. A tool for looking back a little, not an
// archive.
const logCap = 10 << 20

// newLogger logs as text to stderr and to <dir>/fh.log (mode 0600). It
// never receives the token or message text: callers log events and IDs.
func newLogger(dir string, stderr io.Writer, verbose bool) (*slog.Logger, io.Closer, error) {
	f, err := openCapped(filepath.Join(dir, "fh.log"), logCap)
	if err != nil {
		return nil, nil, err
	}
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(io.MultiWriter(stderr, f), &slog.HandlerOptions{Level: level})
	return slog.New(h), f, nil
}

type cappedFile struct {
	mu   sync.Mutex
	f    *os.File
	size int64
	cap  int64
}

func openCapped(path string, limit int64) (*cappedFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	c := &cappedFile{f: f, cap: limit}
	if info, err := f.Stat(); err == nil {
		c.size = info.Size()
	}
	return c, nil
}

func (c *cappedFile) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size+int64(len(p)) > c.cap {
		if err := c.f.Truncate(0); err != nil {
			return 0, err
		}
		c.size = 0
	}
	n, err := c.f.Write(p)
	c.size += int64(n)
	return n, err
}

func (c *cappedFile) Close() error { return c.f.Close() }
