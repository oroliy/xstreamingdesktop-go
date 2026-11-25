package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

func TestEncodeMicrophoneFromSyntheticAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var samples []pionmedia.Sample
	err := EncodeMicrophone(ctx, []string{"-f", "lavfi", "-i", "sine=frequency=440:duration=0.2"}, func(sample pionmedia.Sample) error {
		samples = append(samples, sample)
		return nil
	})
	if err != nil {
		mediaTestUnavailable(t, "Opus encoding unavailable: %v", err)
	}
	if len(samples) < 8 || len(samples) > 12 {
		t.Fatalf("unexpected sample count: %d", len(samples))
	}
	for _, sample := range samples {
		if len(sample.Data) == 0 || sample.Duration != 20*time.Millisecond {
			t.Fatalf("invalid Opus sample: %+v", sample)
		}
	}
}

func TestEncodeMicrophoneStopsOnCallbackFailure(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	failure := errors.New("audio sink failed")
	err := EncodeMicrophone(ctx, []string{"-f", "lavfi", "-i", "sine=frequency=440"}, func(pionmedia.Sample) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("expected sink error, got %v", err)
	}
}

func TestEncodeMicrophoneEmitsDuringLiveInput(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gotSample := errors.New("received live sample")
	err := EncodeMicrophone(ctx, []string{"-re", "-f", "lavfi", "-i", "sine=frequency=440:duration=30"}, func(pionmedia.Sample) error {
		return gotSample
	})
	if !errors.Is(err, gotSample) {
		t.Fatalf("encoder did not produce audio while input remained open: %v", err)
	}
}

// The configured server must be isolated and use a synthetic default source.
func TestEncodeMicrophoneFromVirtualPulseAudio(t *testing.T) {
	server := os.Getenv("XSTREAMING_TEST_PULSE_SERVER")
	if server == "" {
		t.Skip("no isolated PulseAudio test server configured")
	}
	t.Setenv("PULSE_SERVER", server)
	for _, source := range []string{"default", "virtual_mic"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		gotSample := errors.New("received virtual microphone sample")
		err := EncodeMicrophone(ctx, []string{"-f", "pulse", "-i", source}, func(pionmedia.Sample) error {
			return gotSample
		})
		cancel()
		if !errors.Is(err, gotSample) {
			t.Fatalf("virtual PulseAudio capture %q failed: %v", source, err)
		}
	}
}

func TestEncodeMicrophoneStopsOnCancellation(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := EncodeMicrophone(ctx, []string{"-f", "lavfi", "-i", "sine=frequency=440"}, func(pionmedia.Sample) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestEncodeMicrophoneReportsInputFailure(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := EncodeMicrophone(ctx, []string{"-f", "lavfi", "-i", "no_such_xstreaming_filter"}, func(pionmedia.Sample) error {
		t.Fatal("unexpected audio")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "no_such_xstreaming_filter") {
		t.Fatalf("missing ffmpeg input error: %v", err)
	}
}

func TestReadOpusPacketsRejectsInvalidData(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not an Ogg stream"), append([]byte("OggS"), make([]byte, 23)...)} {
		if err := readOpusPackets(bytes.NewReader(data), func([]byte) error { return nil }); err == nil {
			t.Fatalf("accepted invalid input %x", data)
		}
	}
}

func TestReadOpusPacketsHandlesLacingAndContinuation(t *testing.T) {
	page := func(index uint32, flags byte, segments []byte, payload []byte) []byte {
		header := make([]byte, 27)
		copy(header, "OggS")
		header[5] = flags
		binary.LittleEndian.PutUint32(header[14:], 7)
		binary.LittleEndian.PutUint32(header[18:], index)
		header[26] = byte(len(segments))
		return append(append(header, segments...), payload...)
	}
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[9] = 1
	data := page(0, 2, []byte{19}, head)
	data = append(data, page(1, 0, []byte{8}, []byte("OpusTags"))...)
	data = append(data, page(2, 0, []byte{255}, bytes.Repeat([]byte{1}, 255))...)
	data = append(data, page(3, 1, []byte{2, 3}, []byte{2, 2, 3, 3, 3})...)
	var packets [][]byte
	if err := readOpusPackets(bytes.NewReader(data), func(packet []byte) error {
		packets = append(packets, packet)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(packets) != 2 || len(packets[0]) != 257 || !bytes.Equal(packets[0][255:], []byte{2, 2}) || !bytes.Equal(packets[1], []byte{3, 3, 3}) {
		t.Fatalf("bad packet reconstruction: %v", packets)
	}
}
