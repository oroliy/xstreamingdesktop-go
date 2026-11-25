package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/go-client/peer"
	"github.com/oroliy/xstreamingdesktop-go/go-client/protocol"
	"github.com/pion/webrtc/v4"
)

func TestSystemKeyboardRoundTripOverWebRTC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	local, err := peer.New(webrtc.Configuration{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	requests := make(chan *peer.SystemUIRequest, 1)
	errors := make(chan error, 8)
	local.StartControlWithUI(ctx, func(err error) { errors <- err }, func(r *peer.SystemUIRequest) { requests <- r })
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	responses := make(chan protocol.Envelope, 1)
	remote.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "message" {
			return
		}
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			var envelope protocol.Envelope
			if json.Unmarshal(msg.Data, &envelope) != nil {
				return
			}
			if envelope.Type == "Handshake" {
				_ = dc.SendText(`{"type":"HandshakeAck","version":"messageV1"}`)
				payload := protocol.Envelope{Type: "TransactionStart", ID: "keyboard", CV: "server.4", Target: peer.SystemUIVirtualKeyboard, Content: `{"TitleText":"Enter name","MaxLength":3}`}
				b, _ := json.Marshal(payload)
				_ = dc.SendText(string(b))
			}
			if envelope.Type == "TransactionComplete" {
				responses <- envelope
			}
		})
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
	var request *peer.SystemUIRequest
	select {
	case request = <-requests:
	case err := <-errors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	prompt, err := parseSystemPrompt(request)
	if err != nil {
		t.Fatal(err)
	}
	prompt.streamCtx = ctx
	a := &app{streamCtx: ctx, streamCancel: cancel}
	a.enqueueSystemPrompt(prompt)
	tt := ui.NewTester(a.view, 1024, 768)
	if !tt.HasText("Enter name") {
		t.Fatal("keyboard prompt was not shown")
	}
	tt.Type("玩家四五")
	if err := tt.Click("Submit"); err != nil {
		t.Fatal(err)
	}
	select {
	case reply := <-responses:
		var value map[string]string
		if reply.ID != "keyboard" || reply.CV != "server.6" || json.Unmarshal([]byte(reply.Content), &value) != nil || value["Text"] != "玩家四" {
			t.Fatalf("unexpected response: %+v", reply)
		}
	case err := <-errors:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("system keyboard response was not received")
	}
}
