// Package media contains stream media decoding helpers.
package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
)

// VideoDecoder decodes an Annex B H.264 stream with an installed ffmpeg.
// Frames are delivered serially on a decoder goroutine; callbacks must return promptly.
type VideoDecoder struct {
	cmd          *exec.Cmd
	input        io.WriteCloser
	done         chan struct{}
	mu           sync.Mutex
	err          error
	once         sync.Once
	closeTimeout time.Duration
}

func NewVideoDecoder(ctx context.Context, onFrame func(image.Image)) (*VideoDecoder, error) {
	if onFrame == nil {
		return nil, errors.New("frame callback required")
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-probesize", "32", "-analyzeduration", "0", "-flags", "low_delay", "-f", "h264", "-i", "pipe:0", "-an", "-f", "image2pipe", "-vcodec", "ppm", "pipe:1")
	configureMediaProcess(cmd)
	return newVideoDecoder(cmd, onFrame)
}

func newVideoDecoder(cmd *exec.Cmd, onFrame func(image.Image)) (*VideoDecoder, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	diagnostic := &diagnosticTail{}
	cmd.Stderr = diagnostic
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	d := &VideoDecoder{cmd: cmd, input: stdin, done: make(chan struct{}), closeTimeout: 10 * time.Second}
	go func() {
		defer close(d.done)
		reader := bufio.NewReaderSize(stdout, 64<<10)
		for {
			if _, e := reader.Peek(1); errors.Is(e, io.EOF) {
				break
			} else if e != nil {
				d.mu.Lock()
				d.err = e
				d.mu.Unlock()
				_ = cmd.Process.Kill()
				break
			}
			img, e := readPPMFrame(reader)
			if e != nil {
				d.mu.Lock()
				d.err = fmt.Errorf("decode frame: %w", e)
				d.mu.Unlock()
				_ = cmd.Process.Kill()
				break
			}
			onFrame(img)
		}
		err := cmd.Wait()
		d.mu.Lock()
		if err != nil {
			d.err = errors.Join(d.err, processError("ffmpeg", err, diagnostic))
		}
		d.mu.Unlock()
	}()
	return d, nil
}
func (d *VideoDecoder) Write(b []byte) (int, error) { return d.input.Write(b) }

// Close signals end of input and waits for all decoded frames.
func (d *VideoDecoder) Close() error {
	d.once.Do(func() {
		_ = d.input.Close()
		timer := time.NewTimer(d.closeTimeout)
		defer timer.Stop()
		select {
		case <-d.done:
		case <-timer.C:
			// Give an already-exited process precedence at the deadline.
			select {
			case <-d.done:
			default:
				_ = d.cmd.Process.Kill()
				<-d.done
				d.mu.Lock()
				d.err = errors.Join(d.err, errors.New("video decoder did not exit after input closed"))
				d.mu.Unlock()
			}
		}
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// ReceiveH264 reads RTP from a Pion track and writes depacketized H.264 samples.
// RTP loss and reordering are handled by Pion's bounded sample builder.
func ReceiveH264(track *webrtc.TrackRemote, out io.Writer) error {
	return receiveH264(track, out)
}

type rtpReader interface {
	ReadRTP() (*rtp.Packet, interceptor.Attributes, error)
}

func receiveH264(track rtpReader, out io.Writer) error {
	builder := samplebuilder.New(128, &codecs.H264Packet{}, 90000)
	drain := func() error {
		for {
			sample := builder.Pop()
			if sample == nil {
				return nil
			}
			n, err := out.Write(sample.Data)
			if err != nil {
				return err
			}
			if n != len(sample.Data) {
				return io.ErrShortWrite
			}
		}
	}
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			if errors.Is(err, io.EOF) {
				builder.Flush()
				return drain()
			}
			return err
		}
		builder.Push(packet)
		if err = drain(); err != nil {
			return err
		}
	}
}
