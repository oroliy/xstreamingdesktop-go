package controller

import (
	"context"
	"testing"
	"time"

	"github.com/oroliy/xstreamingdesktop-go/peer"
	"github.com/pion/webrtc/v4"
)

func TestVibrationFeedbackOverLocalDataChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	local, err := peer.New(webrtc.Configuration{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	feedback := vibrationFeedback(local.Channel("input"), true)
	remoteChannel := make(chan *webrtc.DataChannel, 1)
	remote.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() == "input" {
			dc.OnOpen(func() { remoteChannel <- dc })
		}
	})
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
	var dc *webrtc.DataChannel
	select {
	case dc = <-remoteChannel:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = dc.Send([]byte{128, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err = dc.Send([]byte{128, 0, 0, 2, 80, 30, 0, 0, 0xe8, 0x03, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-feedback.ready:
		messages := feedback.take()
		if len(messages) != 1 {
			t.Fatalf("got %d feedback messages", len(messages))
		}
		v := messages[0].value
		if v.Index != 2 || v.LeftMotor != 0.8 || v.RightMotor != 0.3 || v.Duration != 100*time.Millisecond {
			t.Fatalf("unexpected vibration: %+v", v)
		}
	case <-ctx.Done():
		t.Fatal("vibration feedback not received")
	}
}
