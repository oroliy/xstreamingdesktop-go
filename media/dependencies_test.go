package media

import (
	"errors"
	"strings"
	"testing"
)

func TestMissingDependencies(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := CheckDependencies()
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatal(err)
	}
}
func TestDiagnosticTail(t *testing.T) {
	var d diagnosticTail
	_, _ = d.Write([]byte(strings.Repeat("a", 5000)))
	_, _ = d.Write([]byte("error detail"))
	got := d.String()
	if len(got) != 4096 || !strings.HasSuffix(got, "error detail") {
		t.Fatalf("tail length %d", len(got))
	}
	err := processError("decoder", errors.New("exit 1"), &d)
	if !strings.Contains(err.Error(), "error detail") {
		t.Fatal(err)
	}
}
