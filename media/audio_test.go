package media

import (
	"context"
	"errors"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAudioPlayer(t *testing.T) {
	if _, err := exec.LookPath("ffplay"); err != nil {
		mediaTestUnavailable(t, "ffplay unavailable: %v", err)
	}
	t.Setenv("SDL_AUDIODRIVER", "dummy")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := NewAudioPlayer(ctx, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		err = p.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: uint16(i), Timestamp: uint32(i * 960)}, Payload: []byte{0xf8, 0xff, 0xfe}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestAudioChannels(t *testing.T) {
	if _, err := NewAudioPlayer(context.Background(), 3, 100); err == nil {
		t.Fatal("accepted invalid channel count")
	}
}

func TestAudioVolume(t *testing.T) {
	if _, err := NewAudioPlayer(context.Background(), 2, 101); err == nil {
		t.Fatal("accepted invalid volume")
	}
}

func TestAudioPlayerStopsWhenProcessIgnoresEOF(t *testing.T) {
	if os.Getenv("XSTREAMING_STUCK_AUDIO") == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAudioPlayerStopsWhenProcessIgnoresEOF$")
	cmd.Env = append(os.Environ(), "XSTREAMING_STUCK_AUDIO=1")
	p, err := newAudioPlayer(cmd, 2)
	if err != nil {
		t.Fatal(err)
	}
	p.closeTimeout = 50 * time.Millisecond
	err = p.Close()
	if err == nil || !strings.Contains(err.Error(), "audio player did not exit") {
		t.Fatalf("expected audio player timeout, got %v", err)
	}
	if err2 := p.Close(); err2 == nil || err2.Error() != err.Error() {
		t.Fatalf("repeat Close returned %v, first returned %v", err2, err)
	}
}

type collectedOpus struct{ sequences []uint16 }

func (w *collectedOpus) WriteRTP(packet *rtp.Packet) error {
	w.sequences = append(w.sequences, packet.SequenceNumber)
	return nil
}

func TestReceiveOpusReordersAndDropsDuplicates(t *testing.T) {
	for _, tc := range []struct {
		input []uint16
		want  []uint16
	}{
		{[]uint16{10, 12, 11, 12, 13}, []uint16{10, 11, 12, 13}},
		{[]uint16{65534, 0, 65535, 1}, []uint16{65534, 65535, 0, 1}},
		{[]uint16{10, 12, 13, 14}, []uint16{10, 12, 13, 14}},
		{[]uint16{10, 12}, []uint16{10, 12}},
		{[]uint16{10, 100}, []uint16{10, 100}},
	} {
		reader := &packetReader{err: io.EOF}
		for _, seq := range tc.input {
			reader.packets = append(reader.packets, &rtp.Packet{Header: rtp.Header{SequenceNumber: seq}})
		}
		var writer collectedOpus
		if err := receiveOpus(reader, &writer); err != nil || !reflect.DeepEqual(writer.sequences, tc.want) {
			t.Fatalf("input=%v got=%v want=%v error=%v", tc.input, writer.sequences, tc.want, err)
		}
	}
}

type failingOpus struct{ err error }

func (w failingOpus) WriteRTP(*rtp.Packet) error { return w.err }

func TestReceiveOpusPropagatesWriterError(t *testing.T) {
	failure := errors.New("audio sink failed")
	reader := &packetReader{packets: []*rtp.Packet{{Header: rtp.Header{SequenceNumber: 1}}}, err: io.EOF}
	if err := receiveOpus(reader, failingOpus{err: failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

type timedPacketReader struct {
	packets  chan *rtp.Packet
	deadline time.Time
}

func (r *timedPacketReader) SetReadDeadline(deadline time.Time) error {
	r.deadline = deadline
	return nil
}
func (r *timedPacketReader) ReadRTP() (*rtp.Packet, interceptor.Attributes, error) {
	if r.deadline.IsZero() {
		packet, ok := <-r.packets
		if !ok {
			return nil, nil, io.EOF
		}
		return packet, nil, nil
	}
	timer := time.NewTimer(time.Until(r.deadline))
	defer timer.Stop()
	select {
	case packet, ok := <-r.packets:
		if !ok {
			return nil, nil, io.EOF
		}
		return packet, nil, nil
	case <-timer.C:
		return nil, nil, os.ErrDeadlineExceeded
	}
}

type notifiedOpus chan uint16

func (w notifiedOpus) WriteRTP(packet *rtp.Packet) error { w <- packet.SequenceNumber; return nil }

func TestReceiveOpusFlushesGapOnDeadline(t *testing.T) {
	reader := &timedPacketReader{packets: make(chan *rtp.Packet, 2)}
	reader.packets <- &rtp.Packet{Header: rtp.Header{SequenceNumber: 10}}
	reader.packets <- &rtp.Packet{Header: rtp.Header{SequenceNumber: 12}}
	out := make(notifiedOpus, 2)
	done := make(chan error, 1)
	go func() { done <- receiveOpus(reader, out) }()
	for _, expected := range []uint16{10, 12} {
		select {
		case got := <-out:
			if got != expected {
				t.Fatalf("got %d, want %d", got, expected)
			}
		case <-time.After(time.Second):
			t.Fatal("buffered audio did not resume")
		}
	}
	reader.packets <- &rtp.Packet{Header: rtp.Header{SequenceNumber: 13}}
	select {
	case got := <-out:
		if got != 13 {
			t.Fatalf("got %d after timeout, want 13", got)
		}
	case <-time.After(time.Second):
		t.Fatal("audio did not resume after timeout")
	}
	close(reader.packets)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type duplicatePacketReader struct {
	count int
	stop  chan struct{}
}

func (*duplicatePacketReader) SetReadDeadline(time.Time) error { return nil }
func (r *duplicatePacketReader) ReadRTP() (*rtp.Packet, interceptor.Attributes, error) {
	r.count++
	if r.count == 1 {
		return &rtp.Packet{Header: rtp.Header{SequenceNumber: 10}}, nil, nil
	}
	if r.count > 2 {
		select {
		case <-r.stop:
			return nil, nil, io.EOF
		case <-time.After(time.Millisecond):
		}
	}
	return &rtp.Packet{Header: rtp.Header{SequenceNumber: 12}}, nil, nil
}

func TestReceiveOpusGapDeadlineWithDuplicateTraffic(t *testing.T) {
	reader := &duplicatePacketReader{stop: make(chan struct{})}
	defer close(reader.stop)
	out := make(notifiedOpus, 2)
	done := make(chan error, 1)
	go func() { done <- receiveOpus(reader, out) }()
	for _, expected := range []uint16{10, 12} {
		select {
		case got := <-out:
			if got != expected {
				t.Fatalf("got %d, want %d", got, expected)
			}
		case <-time.After(time.Second):
			t.Fatal("duplicate traffic delayed buffered audio")
		}
	}
}
