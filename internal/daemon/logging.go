package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// InitLogging configures structured file logging for the daemon.
// Logs are written as JSON to logDir/daemon.log with size-based
// rotation (one backup). A secondary text handler writes to stderr
// (visible in foreground/debug runs; silent when detached).
//
// When debug is true, the log level is set to Debug so all diagnostic
// output is captured. When false, Info is used.
//
// Must be called after os.MkdirAll(logDir, 0755).
// Init failure does not block daemon startup — stderr-only fallback.
func InitLogging(logDir string, debug bool) {
	const maxBytes = 10 << 20 // 10 MiB

	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	rf, err := newRotatingFile(logDir, maxBytes)
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
		return
	}

	fileHandler := slog.NewJSONHandler(rf, &slog.HandlerOptions{Level: level})
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})

	slog.SetDefault(slog.New(&teeHandler{primary: fileHandler, secondary: stderrHandler}))
}

type teeHandler struct {
	primary   slog.Handler
	secondary slog.Handler
}

func (t *teeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return t.primary.Enabled(ctx, l) || t.secondary.Enabled(ctx, l)
}

func (t *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	_ = t.primary.Handle(ctx, r.Clone())
	_ = t.secondary.Handle(ctx, r.Clone())
	return nil
}

func (t *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{t.primary.WithAttrs(attrs), t.secondary.WithAttrs(attrs)}
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{t.primary.WithGroup(name), t.secondary.WithGroup(name)}
}

type rotatingFile struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
	f        *os.File
	written  int64
}

func newRotatingFile(dir string, maxBytes int64) (*rotatingFile, error) {
	return openRotatingFile(filepath.Join(dir, "daemon.log"), maxBytes)
}

// openRotatingFile opens path for append-only writing. If the file exists but
// cannot be opened (e.g. created by a privileged process with restrictive ACLs),
// it tries to remove and recreate the file; if remove is also denied it falls
// back to a PID-suffixed name so the daemon can always write a log.
func openRotatingFile(path string, maxBytes int64) (*rotatingFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		return finishRotatingFile(path, maxBytes, f)
	}

	// Attempt to remove a privileged-owned file and recreate it.
	_ = os.Remove(path)
	if f2, err2 := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err2 == nil {
		fmt.Fprintf(os.Stderr, "hali: replaced unwritable log file %s\n", path)
		return finishRotatingFile(path, maxBytes, f2)
	}

	// Remove also failed (directory does not grant delete-child). Use a session-
	// specific fallback so we never silently drop logs.
	fallback := fmt.Sprintf("%s.%d", path, os.Getpid())
	f3, err3 := os.OpenFile(fallback, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err3 != nil {
		return nil, err // return the original error
	}
	fmt.Fprintf(os.Stderr, "hali: cannot write to %s (%v); logging to %s instead\n", path, err, fallback)
	return finishRotatingFile(fallback, maxBytes, f3)
}

func finishRotatingFile(path string, maxBytes int64, f *os.File) (*rotatingFile, error) {
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rotatingFile{path: path, maxBytes: maxBytes, f: f, written: fi.Size()}, nil
}

// Write implements io.Writer.
// Writes never return an error — a failing write silently discards data
// rather than causing slog to abandon the handler.
func (w *rotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.written >= w.maxBytes {
		_ = w.f.Close()
		_ = os.Rename(w.path, w.path+".1")
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			w.written = 0
			return len(p), nil
		}
		w.f = f
		w.written = 0
	}

	n, _ := w.f.Write(p)
	w.written += int64(n)
	return len(p), nil
}
