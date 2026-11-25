package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

func TestVideoDecoder(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	encoded, err := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=32x32:r=10:d=1", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-f", "h264", "pipe:1").Output()
	if err != nil {
		mediaTestUnavailable(t, "H.264 encoder unavailable: %v", err)
	}
	frames := make(chan image.Image, 20)
	decoder, err := NewVideoDecoder(ctx, func(frame image.Image) { frames <- frame })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decoder.Write(encoded); err != nil {
		t.Fatal(err)
	}
	if err = decoder.Close(); err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no decoded frames")
	}
	frame := <-frames
	if frame.Bounds().Dx() != 32 || frame.Bounds().Dy() != 32 {
		t.Fatal(frame.Bounds())
	}
	r, g, b, _ := frame.At(0, 0).RGBA()
	if r < 50000 || g > 5000 || b > 5000 {
		t.Fatalf("unexpected decoded color: %d %d %d", r, g, b)
	}
}

// The decoder must emit frames while the input remains open for a live stream.
func TestVideoDecoderStreamsBeforeEOF(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		mediaTestUnavailable(t, "ffmpeg unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	encoded, err := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=s=128x128:r=30:d=1", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-g", "30", "-f", "h264", "pipe:1").Output()
	if err != nil {
		mediaTestUnavailable(t, "H.264 encoder unavailable: %v", err)
	}
	frames := make(chan struct{}, 1)
	decoder, err := NewVideoDecoder(ctx, func(image.Image) {
		select {
		case frames <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for i := 0; i < 5; i++ {
			if _, err := decoder.Write(encoded); err != nil {
				return
			}
		}
	}()
	select {
	case <-frames:
	case <-ctx.Done():
		t.Error("decoder produced no frame before end of input")
	}
	cancel()
	<-writerDone
	_ = decoder.Close()
}

func mediaTestUnavailable(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("XSTREAMING_REQUIRE_MEDIA_TESTS") == "1" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

// A broken decoder that continues writing must not make Close hang after a bad frame.
func TestVideoDecoderStopsAfterInvalidFrame(t *testing.T) {
	if os.Getenv("XSTREAMING_BAD_DECODER") == "1" {
		for {
			if _, err := os.Stdout.Write([]byte("invalid frame data")); err != nil {
				os.Exit(0)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestVideoDecoderStopsAfterInvalidFrame$")
	cmd.Env = append(os.Environ(), "XSTREAMING_BAD_DECODER=1")
	decoder, err := newVideoDecoder(cmd, func(image.Image) { t.Error("unexpected frame") })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- decoder.Close() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "decode frame") {
			t.Fatalf("expected frame decode error, got %v", err)
		}
	case <-ctx.Done():
		t.Fatal("decoder did not stop after invalid output")
	}
}

func TestVideoDecoderStopsWhenProcessIgnoresEOF(t *testing.T) {
	if os.Getenv("XSTREAMING_STUCK_DECODER") == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestVideoDecoderStopsWhenProcessIgnoresEOF$")
	cmd.Env = append(os.Environ(), "XSTREAMING_STUCK_DECODER=1")
	decoder, err := newVideoDecoder(cmd, func(image.Image) { t.Error("unexpected frame") })
	if err != nil {
		t.Fatal(err)
	}
	decoder.closeTimeout = 50 * time.Millisecond
	err = decoder.Close()
	if err == nil || !strings.Contains(err.Error(), "video decoder did not exit") {
		t.Fatalf("expected decoder timeout, got %v", err)
	}
	if err2 := decoder.Close(); err2 == nil || err2.Error() != err.Error() {
		t.Fatalf("repeat Close returned %v, first returned %v", err2, err)
	}
}

type packetReader struct {
	packets []*rtp.Packet
	err     error
}

func (r *packetReader) ReadRTP() (*rtp.Packet, interceptor.Attributes, error) {
	if len(r.packets) == 0 {
		return nil, nil, r.err
	}
	p := r.packets[0]
	r.packets = r.packets[1:]
	return p, nil, nil
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestReceiveH264FlushesLastSample(t *testing.T) {
	packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 100, Marker: true}, Payload: []byte{0x65, 1, 2}}
	r := &packetReader{packets: []*rtp.Packet{packet}, err: io.EOF}
	var out bytes.Buffer
	if err := receiveH264(r, &out); err != nil {
		t.Fatal(err)
	}
	if want := []byte{0, 0, 0, 1, 0x65, 1, 2}; !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("got %x, want %x", out.Bytes(), want)
	}
}

func TestReceiveH264ReportsShortWrite(t *testing.T) {
	packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 100, Marker: true}, Payload: []byte{0x65, 1, 2}}
	r := &packetReader{packets: []*rtp.Packet{packet}, err: io.EOF}
	if err := receiveH264(r, shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("got %v", err)
	}
}
