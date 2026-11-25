package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

// EncodeMicrophone runs ffmpeg on the caller-selected input and emits mono,
// 20 ms Opus samples. The caller controls when to start and cancel capture.
// inputArgs must describe an ffmpeg input, including its -i argument.
func EncodeMicrophone(ctx context.Context, inputArgs []string, send func(pionmedia.Sample) error) error {
	if len(inputArgs) == 0 || send == nil {
		return errors.New("audio input and sample callback are required")
	}
	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := append([]string{"-hide_banner", "-loglevel", "error"}, inputArgs...)
	args = append(args, "-vn", "-ac", "1", "-ar", "48000", "-c:a", "libopus", "-frame_duration", "20", "-application", "voip", "-b:a", "32k", "-f", "ogg", "-page_duration", "20000", "-flush_packets", "1", "pipe:1")
	cmd := exec.CommandContext(processCtx, "ffmpeg", args...)
	configureMediaProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	diagnostic := &diagnosticTail{}
	cmd.Stderr = diagnostic
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start microphone encoder: %w", err)
	}
	var sinkErr error
	readErr := readOpusPackets(stdout, func(packet []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		sinkErr = send(pionmedia.Sample{Data: packet, Duration: 20 * time.Millisecond})
		return sinkErr
	})
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if sinkErr != nil {
		return sinkErr
	}
	if readErr != nil {
		if waitErr != nil && diagnostic.String() != "" {
			return errors.Join(readErr, processError("microphone encoder", waitErr, diagnostic))
		}
		return readErr
	}
	if waitErr != nil {
		return processError("microphone encoder", waitErr, diagnostic)
	}
	return nil
}

// readOpusPackets reconstructs packet boundaries from Ogg lacing values.
// Ogg pages can contain multiple packets or parts of a packet.
func readOpusPackets(r io.Reader, send func([]byte) error) error {
	const maxPacket = 256 << 10
	var pending []byte
	var streamID, sequence uint32
	started, head, tags, audio := false, false, false, false
	for {
		var header [27]byte
		_, err := io.ReadFull(r, header[:])
		if errors.Is(err, io.EOF) {
			if len(pending) != 0 || !head || !tags || !audio {
				return errors.New("incomplete Opus stream")
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("read Ogg page: %w", err)
		}
		if string(header[:4]) != "OggS" || header[4] != 0 {
			return errors.New("invalid Ogg page")
		}
		serial := binary.LittleEndian.Uint32(header[14:18])
		page := binary.LittleEndian.Uint32(header[18:22])
		if !started {
			if header[5]&2 == 0 {
				return errors.New("missing Ogg stream start")
			}
			streamID, sequence, started = serial, page, true
		} else if serial != streamID || page != sequence+1 {
			return errors.New("unexpected Ogg stream or page sequence")
		} else {
			sequence = page
		}
		if (header[5]&1 != 0) != (len(pending) > 0) {
			return errors.New("invalid Ogg packet continuation")
		}
		segments := make([]byte, int(header[26]))
		if _, err = io.ReadFull(r, segments); err != nil {
			return fmt.Errorf("read Ogg segments: %w", err)
		}
		for _, size := range segments {
			if len(pending)+int(size) > maxPacket {
				return errors.New("Opus packet too large")
			}
			start := len(pending)
			pending = append(pending, make([]byte, int(size))...)
			if _, err = io.ReadFull(r, pending[start:]); err != nil {
				return fmt.Errorf("read Ogg packet: %w", err)
			}
			if size == 255 {
				continue
			}
			packet := pending
			pending = nil
			if !head {
				if len(packet) < 19 || !bytes.Equal(packet[:8], []byte("OpusHead")) || packet[9] != 1 {
					return errors.New("invalid mono Opus header")
				}
				head = true
				continue
			}
			if !tags {
				if !bytes.HasPrefix(packet, []byte("OpusTags")) {
					return errors.New("missing Opus tags")
				}
				tags = true
				continue
			}
			if len(packet) == 0 {
				return errors.New("empty Opus packet")
			}
			audio = true
			if err = send(packet); err != nil {
				return err
			}
		}
	}
}
