// Package peer provides the WebRTC transport used by the Go client.
package peer

import (
	"context"
	"fmt"
	"strings"

	"github.com/pion/webrtc/v4"
)

type Peer struct {
	ready    chan struct{}
	conn     *webrtc.PeerConnection
	audio    *webrtc.RTPTransceiver
	channels map[string]*webrtc.DataChannel
}

// New creates the media transceivers and the channels used by xstreaming-player.
// onTrack must consume the track's RTP packets until ReadRTP returns an error.
func New(config webrtc.Configuration, onTrack func(*webrtc.TrackRemote, *webrtc.RTPReceiver)) (*Peer, error) {
	conn, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	p := &Peer{conn: conn, channels: make(map[string]*webrtc.DataChannel), ready: make(chan struct{})}
	fail := func(err error) (*Peer, error) { _ = conn.Close(); return nil, err }
	if onTrack != nil {
		conn.OnTrack(onTrack)
	}
	for _, channel := range []struct{ name, protocol string }{{"input", "1.0"}, {"chat", "chatV1"}, {"control", "controlV1"}, {"message", "messageV1"}} {
		protocol := channel.protocol
		dc, err := conn.CreateDataChannel(channel.name, &webrtc.DataChannelInit{Protocol: &protocol})
		if err != nil {
			return fail(err)
		}
		p.channels[channel.name] = dc
	}
	audio, err := conn.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv})
	if err != nil {
		return fail(err)
	}
	if err = audio.SetCodecPreferences([]webrtc.RTPCodecParameters{{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1"}, PayloadType: 111}}); err != nil {
		return fail(err)
	}
	p.audio = audio
	video, err := conn.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
	if err != nil {
		return fail(err)
	}
	feedback := []webrtc.RTCPFeedback{{Type: "goog-remb"}, {Type: "ccm", Parameter: "fir"}, {Type: "nack"}, {Type: "nack", Parameter: "pli"}}
	var h264 []webrtc.RTPCodecParameters
	for _, profile := range []struct {
		fmtp    string
		payload webrtc.PayloadType
	}{{"42001f", 102}, {"42e01f", 106}, {"4d001f", 127}} {
		h264 = append(h264, webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=" + profile.fmtp, RTCPFeedback: feedback}, PayloadType: profile.payload})
	}
	if err = video.SetCodecPreferences(h264); err != nil {
		return fail(err)
	}
	return p, nil
}

// Offer gathers candidates until completion; cancellation leaves the peer open for Close.
func (p *Peer) Offer(ctx context.Context) (webrtc.SessionDescription, error) {
	offer, err := p.conn.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	done := webrtc.GatheringCompletePromise(p.conn)
	if err = p.conn.SetLocalDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	select {
	case <-ctx.Done():
		return webrtc.SessionDescription{}, ctx.Err()
	case <-done:
	}
	return *p.conn.LocalDescription(), nil
}
func (p *Peer) SetAnswer(sdp string) error {
	if sdp == "" {
		return fmt.Errorf("empty SDP answer")
	}
	return p.conn.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
}
func (p *Peer) AddCandidate(candidate string, mid string, line uint16) error {
	if candidate == "" || candidate == "a=end-of-candidates" || candidate == "end-of-candidates" {
		return nil
	}
	candidate = strings.TrimPrefix(candidate, "a=")
	init := webrtc.ICECandidateInit{Candidate: candidate, SDPMLineIndex: &line}
	if mid != "" {
		init.SDPMid = &mid
	}
	return p.conn.AddICECandidate(init)
}

// OnConnectionStateChange registers a callback for transport state changes.
func (p *Peer) OnConnectionStateChange(fn func(webrtc.PeerConnectionState)) {
	p.conn.OnConnectionStateChange(fn)
}
func (p *Peer) Channel(name string) *webrtc.DataChannel { return p.channels[name] }
func (p *Peer) Close() error                            { return p.conn.Close() }

// ReplaceMicrophone attaches an Opus sample track or detaches it when nil.
// It does not access any recording device.
func (p *Peer) ReplaceMicrophone(track *webrtc.TrackLocalStaticSample) error {
	if track == nil {
		return p.audio.Sender().ReplaceTrack(nil)
	}
	return p.audio.Sender().ReplaceTrack(track)
}
