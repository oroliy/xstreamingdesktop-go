package peer

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pion/webrtc/v4"
	"testing"
	"time"
)

func TestControlHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := New(webrtc.Configuration{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	errors := make(chan error, 4)
	uiErrors := make(chan error, 1)
	p.StartControlWithUI(ctx, func(err error) { errors <- err }, func(request *SystemUIRequest) {
		uiErrors <- request.Complete(map[string]string{"Text": "hello"})
	})
	remote, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	got := make(chan string, 16)
	replies := make(chan string, 1)
	uiReplies := make(chan map[string]any, 1)
	remote.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			var body map[string]any
			if json.Unmarshal(msg.Data, &body) != nil {
				return
			}
			if dc.Label() == "message" && body["type"] == "Handshake" {
				_ = dc.SendText(`{"type":"HandshakeAck","version":"messageV1"}`)
				_ = dc.SendText(`{"type":"TransactionStart","id":"t1","target":"/unsupported","cv":"server.2"}`)
				_ = dc.SendText(`{"type":"TransactionStart","id":"ui1","target":"/streaming/systemUi/messages/ShowVirtualKeyboard","content":"{}","cv":"ui.1"}`)
			}
			if dc.Label() == "message" && body["type"] == "Unhandled" {
				replies <- body["cv"].(string)
			}
			if dc.Label() == "message" && body["type"] == "TransactionComplete" {
				uiReplies <- body
			}
			if dc.Label() == "control" {
				if name, ok := body["message"].(string); ok {
					got <- name
				}
			}
		})
	})
	offer, err := p.Offer(ctx)
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
	if err = p.SetAnswer(remote.LocalDescription().SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-uiErrors:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case reply := <-uiReplies:
		if reply["id"] != "ui1" || reply["cv"] != "ui.3" || reply["content"] != `{"Text":"hello"}` {
			t.Fatalf("unexpected system UI reply: %v", reply)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case cv := <-replies:
		if cv != "server.3" {
			t.Fatal(cv)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, expected := range []string{"authorizationRequest", "gamepadChanged"} {
		select {
		case msg := <-got:
			if msg != expected {
				t.Fatalf("got %q want %q", msg, expected)
			}
		case err := <-errors:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case <-p.Ready():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case msg := <-got:
		if msg != "videoKeyframeRequested" {
			t.Fatalf("got %q, want initial keyframe request", msg)
		}
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("initial keyframe request did not follow handshake")
	}
}

func TestKeyframeRequestsRecoverAfterSendFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sendError := errors.New("temporarily unavailable")
	attempts, reports := 0, 0
	requestKeyframes(ctx, time.Millisecond, func() error {
		attempts++
		switch attempts {
		case 1, 2:
			return sendError
		case 3:
			return nil
		default:
			cancel()
			return nil
		}
	}, func(err error) {
		if !errors.Is(err, sendError) {
			t.Fatalf("unexpected error: %v", err)
		}
		reports++
	})
	if attempts != 4 || reports != 1 {
		t.Fatalf("attempts=%d reports=%d", attempts, reports)
	}
}

func TestKeyframeRequestsStopWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requestKeyframes(ctx, time.Millisecond, func() error {
		t.Fatal("sent request after cancellation")
		return nil
	}, func(error) { t.Fatal("reported after cancellation") })
}
