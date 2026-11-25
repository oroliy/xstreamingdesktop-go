package media

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParsePulseSources(t *testing.T) {
	data := "Auto-detected sources for pulse:\n* alsa_input.usb [USB Microphone] (none)\n  virtual_mic [Null Input] (none)\n  alsa_input.usb [Duplicate] (none)\n  invalid name [Bad] (none)\n"
	sources, err := parsePulseSources(data)
	if err != nil || !reflect.DeepEqual(sources, []PulseSource{{Name: "alsa_input.usb", Description: "USB Microphone"}, {Name: "virtual_mic", Description: "Null Input"}}) {
		t.Fatalf("sources=%v error=%v", sources, err)
	}
	for _, invalid := range []string{"", "Auto-detected sources for pulse:\nCannot list sources: failed\n"} {
		if _, err := parsePulseSources(invalid); err == nil {
			t.Fatalf("accepted source-list error %q", invalid)
		}
	}
}

func TestPulseSourceOutputIsBounded(t *testing.T) {
	var output boundedOutput
	input := bytes.Repeat([]byte{'x'}, 70<<10)
	if n, err := output.Write(input); err != nil || n != len(input) || !output.truncated || output.buf.Len() != 64<<10 {
		t.Fatalf("written=%d error=%v retained=%d truncated=%v", n, err, output.buf.Len(), output.truncated)
	}
}

func TestPulseSourcesRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PulseSources(ctx); err != context.Canceled {
		t.Fatalf("got %v, want canceled", err)
	}
}

func TestPulseSourcesReportsUnavailableServer(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	t.Setenv("PULSE_SERVER", "unix:"+filepath.Join(t.TempDir(), "missing"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := PulseSources(ctx); err == nil {
		t.Fatal("accepted an unavailable PulseAudio server")
	}
}

func TestPulseSourcesWithVirtualServer(t *testing.T) {
	server := os.Getenv("XSTREAMING_TEST_PULSE_SERVER")
	if server == "" {
		t.Skip("no isolated PulseAudio test server configured")
	}
	t.Setenv("PULSE_SERVER", server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sources, err := PulseSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].Name != "virtual_mic" || sources[0].Description == "" {
		t.Fatalf("sources=%v error=%v", sources, err)
	}
}
