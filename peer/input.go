package peer

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/oroliy/xstreamingdesktop-go/protocol"
	"github.com/pion/webrtc/v4"
)

// InputSender serializes writes and assigns sequence numbers to controller reports.
type InputSender struct {
	mu          sync.Mutex
	channel     *webrtc.DataChannel
	control     *webrtc.DataChannel
	started     time.Time
	seq         uint32
	initialized bool
}

func (p *Peer) Input() *InputSender {
	return &InputSender{channel: p.Channel("input"), control: p.Channel("control"), started: time.Now()}
}
func (s *InputSender) Init(maxTouchpoints uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialized {
		return nil
	}
	if s.channel.ReadyState() != webrtc.DataChannelStateOpen {
		return errors.New("input channel is not open")
	}
	if err := s.channel.Send(protocol.ClientMetadata(s.seq, float64(time.Since(s.started).Microseconds())/1000, maxTouchpoints)); err != nil {
		return err
	}
	s.initialized = true
	return nil
}
func (s *InputSender) SendGamepads(frames []protocol.Gamepad) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return errors.New("input channel is not initialized")
	}
	next := s.seq + 1
	packet, err := protocol.GamepadPacket(next, float64(time.Since(s.started).Microseconds())/1000, frames)
	if err != nil {
		return err
	}
	if err = s.channel.Send(packet); err != nil {
		return err
	}
	s.seq = next
	return nil
}

// Ready closes after the message and control channels complete their initial handshake.
func (p *Peer) Ready() <-chan struct{} { return p.ready }

// GamepadChanged announces a connection or disconnection after the handshake.
func (s *InputSender) GamepadChanged(index uint8, added bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		return errors.New("input channel is not initialized")
	}
	if s.control.ReadyState() != webrtc.DataChannelStateOpen {
		return errors.New("control channel is not open")
	}
	data, err := json.Marshal(protocol.GamepadChanged(int(index), added))
	if err != nil {
		return err
	}
	return s.control.SendText(string(data))
}
