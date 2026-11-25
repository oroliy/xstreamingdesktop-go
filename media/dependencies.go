package media

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// CheckDependencies verifies that the preview's media executables are on PATH.
func CheckDependencies() error {
	for _, name := range []string{"ffmpeg", "ffplay"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("%s is required for streaming; install FFmpeg and add it to PATH: %w", name, err)
		}
	}
	return nil
}

// diagnosticTail retains only a bounded suffix of process stderr.
type diagnosticTail struct {
	mu   sync.Mutex
	data []byte
}

func (b *diagnosticTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	const limit = 4096
	if len(p) >= limit {
		b.data = append(b.data[:0], p[len(p)-limit:]...)
	} else {
		if overflow := len(b.data) + len(p) - limit; overflow > 0 {
			b.data = append(b.data[:0], b.data[overflow:]...)
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *diagnosticTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}
func processError(name string, err error, diagnostic *diagnosticTail) error {
	if err == nil {
		return nil
	}
	if msg := diagnostic.String(); msg != "" {
		return fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return fmt.Errorf("%s: %w", name, err)
}
