package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/oroliy/xstreamingdesktop-go/protocol"
	"github.com/pion/webrtc/v4"
)

// StartControl installs the initial message handshake and control messages.
// Call before negotiating. The caller cancels ctx before closing the peer.
// Unsupported message targets are answered as unhandled.
func (p *Peer) StartControl(ctx context.Context, onError func(error)) {
	p.StartControlWithUI(ctx, onError, nil)
}

// StartControlWithUI additionally dispatches supported system UI requests.
func (p *Peer) StartControlWithUI(ctx context.Context, onError func(error), onUI func(*SystemUIRequest)) {
	message, control := p.Channel("message"), p.Channel("control")
	state := protocol.NewMessageChannel()
	var mu sync.Mutex
	ready, controlOpen, started := false, false, false
	report := func(err error) {
		if err != nil && onError != nil {
			onError(err)
		}
	}
	send := func(dc *webrtc.DataChannel, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return dc.SendText(string(b))
	}
	uiRequests := newSystemUIRequests(ctx, func(v protocol.Envelope) error { return send(message, v) }, onUI)
	start := func() {
		mu.Lock()
		if !ready || !controlOpen || started {
			mu.Unlock()
			return
		}
		started = true
		mu.Unlock()
		if err := send(control, protocol.Authorization()); err != nil {
			report(err)
			return
		}
		if err := send(control, protocol.GamepadChanged(0, false)); err != nil {
			report(err)
			return
		}
		go func() {
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			close(p.ready)
			requestKeyframes(ctx, 5*time.Second, func() error {
				return send(control, protocol.KeyframeRequest())
			}, report)
		}()
	}
	control.OnOpen(func() { mu.Lock(); controlOpen = true; mu.Unlock(); start() })
	message.OnOpen(func() { report(send(message, state.Handshake())) })
	message.OnMessage(func(msg webrtc.DataChannelMessage) {
		data := bytes.ReplaceAll(msg.Data, []byte{0}, nil)
		var envelope protocol.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			report(fmt.Errorf("message channel: %w", err))
			return
		}
		if envelope.Type != "HandshakeAck" {
			if uiRequests.handle(envelope) {
				return
			}
			if reply := state.Incoming(envelope); reply != nil {
				report(send(message, reply))
			}
			return
		}
		config, err := state.HandleHandshake(data)
		if err != nil {
			report(err)
			return
		}
		for _, v := range config {
			if err = send(message, v); err != nil {
				report(err)
				return
			}
		}
		mu.Lock()
		ready = true
		mu.Unlock()
		start()
	})
}

// requestKeyframes sends immediately, then retries at a bounded rate even if
// a transient send error occurs while the connection is recovering.
func requestKeyframes(ctx context.Context, interval time.Duration, send func() error, report func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failed := false
	for {
		if ctx.Err() != nil {
			return
		}
		if err := send(); err != nil {
			if !failed && ctx.Err() == nil {
				report(err)
			}
			failed = true
		} else {
			failed = false
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
