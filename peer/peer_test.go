package peer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oroliy/xstreamingdesktop-go/protocol"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func TestLocalNegotiationAndDataChannel(t *testing.T) {
	local, err := New(webrtc.Configuration{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	closed := make(chan string, 1)
	local.OnCriticalChannelClose(func(name string) {
		select {
		case closed <- name:
		default:
		}
	})
	remoteInput := make(chan *webrtc.DataChannel, 1)
	received := make(chan string, 1)
	remote.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == "input" {
			remoteInput <- dc
			dc.OnMessage(func(msg webrtc.DataChannelMessage) { received <- string(msg.Data) })
		}
	})
	opened := make(chan struct{})
	local.Channel("input").OnOpen(func() { close(opened) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	offer, err := local.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.SetRemoteDescription(offer); err != nil {
		t.Fatal(err)
	}
	answer, err := remote.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(remote)
	if err = remote.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = local.SetAnswer(remote.LocalDescription().SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	sender := local.Input()
	if err = sender.Init(0); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-received:
		if len(data) != 15 || data[0] != 8 {
			t.Fatal("invalid metadata")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = sender.SendGamepads([]protocol.Gamepad{{Buttons: protocol.A}}); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-received:
		if len(data) != 38 || data[0] != 2 {
			t.Fatal("invalid gamepad packet")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = local.Channel("input").SendText("hello"); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-received:
		if msg != "hello" {
			t.Fatal(msg)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	input := <-remoteInput
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-closed:
		if name != "input" {
			t.Fatalf("closed %q", name)
		}
	case <-ctx.Done():
		t.Fatal("input closure not reported")
	}
}
func TestRejectEmptyAnswer(t *testing.T) {
	p, err := New(webrtc.Configuration{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.SetAnswer("") == nil {
		t.Fatal("accepted empty answer")
	}
}

func TestReceiveVideoRTP(t *testing.T) {
	packets := make(chan []byte, 1)
	local, err := New(webrtc.Configuration{}, func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		packet, _, err := track.ReadRTP()
		if err == nil {
			packets <- packet.Payload
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.AddTrack(track); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	offer, err := local.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = remote.SetRemoteDescription(offer); err != nil {
		t.Fatal(err)
	}
	answer, err := remote.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := webrtc.GatheringCompletePromise(remote)
	if err = remote.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = local.SetAnswer(remote.LocalDescription().SDP); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 9000}, Payload: []byte{0x65, 0x01}})
		case data := <-packets:
			if len(data) != 2 || data[0] != 0x65 {
				t.Fatal(data)
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestOfferOnlySupportedCodecs(t *testing.T) {
	p, err := New(webrtc.Configuration{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	offer, err := p.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{"H264/90000", "opus/48000"} {
		if !strings.Contains(offer.SDP, codec) {
			t.Fatalf("missing %s", codec)
		}
	}
	for _, codec := range []string{"VP8/90000", "VP9/90000", "PCMU/8000", "AV1/90000"} {
		if strings.Contains(offer.SDP, codec) {
			t.Fatalf("unexpected %s", codec)
		}
	}
}

func TestDefaultDataChannelsAndSTUN(t *testing.T) {
	config := DefaultRTCConfiguration()
	if len(config.ICEServers) < 2 {
		t.Fatal("missing STUN alternatives")
	}
	for _, server := range config.ICEServers {
		for _, address := range server.URLs {
			if !strings.HasPrefix(address, "stun:") {
				t.Fatalf("unexpected ICE server %q", address)
			}
		}
	}
	p, err := New(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for name, protocol := range map[string]string{"input": "1.0", "chat": "chatV1", "control": "controlV1", "message": "messageV1"} {
		channel := p.Channel(name)
		if channel == nil || channel.Protocol() != protocol || !channel.Ordered() || channel.MaxRetransmits() != nil || channel.MaxPacketLifeTime() != nil {
			t.Fatalf("unexpected %s channel configuration", name)
		}
	}
}
