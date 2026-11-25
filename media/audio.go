package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

// AudioPlayer streams Opus RTP through an Ogg container to ffplay.
// It requires ffplay and an available audio device.
type AudioPlayer struct {
	cmd          *exec.Cmd
	ogg          *oggwriter.OggWriter
	once         sync.Once
	err          error
	diagnostic   *diagnosticTail
	closeTimeout time.Duration
}

func NewAudioPlayer(ctx context.Context, channels uint16, volume int) (*AudioPlayer, error) {
	if channels != 1 && channels != 2 {
		return nil, errors.New("Opus requires one or two channels")
	}
	if volume < 0 || volume > 100 {
		return nil, errors.New("volume must be between 0 and 100")
	}
	cmd := exec.CommandContext(ctx, "ffplay", "-loglevel", "error", "-nodisp", "-autoexit", "-volume", strconv.Itoa(volume), "-probesize", "32", "-analyzeduration", "0", "-i", "pipe:0")
	configureMediaProcess(cmd)
	return newAudioPlayer(cmd, channels)
}

func newAudioPlayer(cmd *exec.Cmd, channels uint16) (*AudioPlayer, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = io.Discard
	diagnostic := &diagnosticTail{}
	cmd.Stderr = diagnostic
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffplay: %w", err)
	}
	writer, err := oggwriter.NewWith(stdin, 48000, channels)
	if err != nil {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		if waitErr := cmd.Wait(); waitErr != nil {
			return nil, errors.Join(err, processError("ffplay", waitErr, diagnostic))
		}
		return nil, err
	}
	return &AudioPlayer{cmd: cmd, ogg: writer, diagnostic: diagnostic, closeTimeout: 10 * time.Second}, nil
}
func (p *AudioPlayer) WriteRTP(packet *rtp.Packet) error { return p.ogg.WriteRTP(packet) }
func (p *AudioPlayer) Close() error {
	p.once.Do(func() {
		done := make(chan error, 1)
		go func() {
			err := p.ogg.Close()
			if waitErr := p.cmd.Wait(); waitErr != nil {
				err = errors.Join(err, processError("ffplay", waitErr, p.diagnostic))
			}
			done <- err
		}()
		timer := time.NewTimer(p.closeTimeout)
		defer timer.Stop()
		select {
		case p.err = <-done:
		case <-timer.C:
			select {
			case p.err = <-done:
			default:
				_ = p.cmd.Process.Kill()
				p.err = errors.Join(<-done, errors.New("audio player did not exit after input closed"))
			}
		}
	})
	return p.err
}

// ReceiveOpus reorders a small number of late packets before writing Opus audio.
// When a packet is lost, it holds at most three subsequent packets or 100 ms.
func ReceiveOpus(track *webrtc.TrackRemote, p *AudioPlayer) error {
	return receiveOpus(track, p)
}

type opusWriter interface {
	WriteRTP(*rtp.Packet) error
}

func receiveOpus(track rtpReader, out opusWriter) error {
	const maxPending = 3
	const maxJump = 64
	pending := make(map[uint16]*rtp.Packet, maxPending)
	var next uint16
	initialized := false
	var gapDeadline time.Time
	deadlineReader, hasDeadline := track.(interface{ SetReadDeadline(time.Time) error })
	if hasDeadline {
		defer func() { _ = deadlineReader.SetReadDeadline(time.Time{}) }()
	}
	drain := func(flush bool) error {
		for len(pending) > 0 {
			if packet, ok := pending[next]; ok {
				if err := out.WriteRTP(packet); err != nil {
					return err
				}
				delete(pending, next)
				next++
				continue
			}
			if !flush && len(pending) < maxPending {
				return nil
			}
			var nearest uint16
			best := int32(1 << 16)
			for seq := range pending {
				distance := int32(uint16(seq - next))
				if distance < best {
					nearest, best = seq, distance
				}
			}
			next = nearest
		}
		return nil
	}
	for {
		if hasDeadline {
			if len(pending) > 0 && gapDeadline.IsZero() {
				gapDeadline = time.Now().Add(100 * time.Millisecond)
				if err := deadlineReader.SetReadDeadline(gapDeadline); err != nil {
					return err
				}
			} else if len(pending) == 0 && !gapDeadline.IsZero() {
				gapDeadline = time.Time{}
				if err := deadlineReader.SetReadDeadline(time.Time{}); err != nil {
					return err
				}
			}
		}
		// A reader can keep returning already-buffered packets after its deadline.
		if !gapDeadline.IsZero() && !time.Now().Before(gapDeadline) {
			if err := drain(true); err != nil {
				return err
			}
			continue
		}
		packet, _, err := track.ReadRTP()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return drain(true)
			}
			var timeout net.Error
			if !gapDeadline.IsZero() && errors.As(err, &timeout) && timeout.Timeout() {
				if err := drain(true); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if packet == nil {
			return errors.New("nil audio packet")
		}
		if !initialized {
			next, initialized = packet.SequenceNumber, true
		}
		distance := int16(packet.SequenceNumber - next)
		if distance < 0 {
			continue
		}
		if distance > maxJump {
			if err := drain(true); err != nil {
				return err
			}
			if int16(packet.SequenceNumber-next) < 0 {
				continue
			}
			next = packet.SequenceNumber
		}
		pending[packet.SequenceNumber] = packet
		if err := drain(false); err != nil {
			return err
		}
	}
}
