package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

// Microphone is one enabled interval of a session's Opus sender.
type Microphone struct {
	session    *Session
	generation uint64
}

// WriteSample rejects stale handles, including after the microphone is restarted.
func (m *Microphone) WriteSample(sample pionmedia.Sample) error {
	s := m.session
	s.micMu.Lock()
	defer s.micMu.Unlock()
	if !s.micOn || m.generation != s.micGeneration || s.live.Err() != nil {
		return errors.New("microphone is not active")
	}
	return s.micTrack.WriteSample(sample)
}

// StartMicrophone negotiates a caller-fed Opus track. It never opens a device.
// Once signaling has started, a failure ends the session because its signaling
// state may no longer match the service. Call StopMicrophone to detach the track.
func (s *Session) StartMicrophone(ctx context.Context) (*Microphone, error) {
	s.micMu.Lock()
	defer s.micMu.Unlock()
	if s.micOn {
		return nil, errors.New("microphone already active")
	}
	if err := s.live.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Reuse the packetizer across mute/unmute. Creating a fresh one resets RTP
	// sequence numbers for the same SSRC, which a receiver can reject as replay.
	track := s.micTrack
	if track == nil {
		var err error
		track, err = webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}, "microphone", "xstreaming")
		if err != nil {
			return nil, err
		}
		s.micTrack = track
	}
	if err := s.peer.ReplaceMicrophone(track); err != nil {
		return nil, fmt.Errorf("attach microphone: %w", err)
	}
	negotiation, cancel := context.WithTimeout(s.live, 60*time.Second)
	defer cancel()
	stopCaller := context.AfterFunc(ctx, cancel)
	defer stopCaller()
	offer, err := s.peer.Offer(negotiation)
	if err == nil {
		var raw json.RawMessage
		raw, err = s.client.ExchangeSDP(negotiation, s.id, offer.SDP, true)
		if err == nil {
			var answer struct {
				SDP string `json:"sdp"`
			}
			err = json.Unmarshal(raw, &answer)
			if err == nil {
				err = s.peer.SetAnswer(answer.SDP)
			}
		}
	}
	if err != nil {
		_ = s.peer.ReplaceMicrophone(nil)
		s.cancel(fmt.Errorf("microphone negotiation failed: %w", err))
		return nil, fmt.Errorf("microphone negotiation failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = s.peer.ReplaceMicrophone(nil)
		return nil, err
	}
	if err := s.live.Err(); err != nil {
		_ = s.peer.ReplaceMicrophone(nil)
		return nil, err
	}
	s.micOn = true
	s.micGeneration++
	return &Microphone{session: s, generation: s.micGeneration}, nil
}

// StopMicrophone detaches the local track so further samples cannot be sent.
func (s *Session) StopMicrophone() error {
	s.micMu.Lock()
	defer s.micMu.Unlock()
	if !s.micOn {
		return nil
	}
	if err := s.peer.ReplaceMicrophone(nil); err != nil {
		return err
	}
	s.micOn = false
	return nil
}
