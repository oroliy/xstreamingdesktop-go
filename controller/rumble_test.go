package controller

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/game/gamepad"
	"github.com/oroliy/xstreamingdesktop-go/protocol"
	"github.com/pion/webrtc/v4"
)

type motorCall struct {
	pad         *gamepad.Pad
	left, right float64
}

func TestXInputSlotUsesOnlyExactMyGoNames(t *testing.T) {
	for name, want := range map[string]int{"Xbox Controller 1": 0, "Xbox Controller 2": 1, "Xbox Controller 3": 2, "Xbox Controller 4": 3} {
		if got, ok := xinputSlot(name); !ok || got != want {
			t.Errorf("%q: got %d, %v; want %d", name, got, ok, want)
		}
	}
	for _, name := range []string{"", "Xbox Controller 0", "Xbox Controller 5", "Xbox Controller 01", "Xbox Controller +1", "Other 1", "Xbox Controller 1 extra"} {
		if slot, ok := xinputSlot(name); ok {
			t.Errorf("accepted %q as slot %d", name, slot)
		}
	}
}

func TestDisconnectedPadCannotUseXInputSlot(t *testing.T) {
	for _, pad := range []*gamepad.Pad{nil, new(gamepad.Pad)} {
		if slot, ok := connectedXInputSlot(pad); ok {
			t.Fatalf("accepted disconnected pad for slot %d", slot)
		}
	}
}

func TestRumbleReplacementDisconnectAndClose(t *testing.T) {
	var mu sync.Mutex
	var calls []motorCall
	m := newRumbleManager(func(pad *gamepad.Pad, left, right float64) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, motorCall{pad, left, right})
	})
	pad := new(gamepad.Pad)
	m.play(0, pad, protocol.Vibration{LeftMotor: 1, Duration: 30 * time.Millisecond})
	m.play(0, pad, protocol.Vibration{RightMotor: 0.5, Duration: time.Second})
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	if len(calls) != 3 || calls[2].right != 0.5 {
		t.Errorf("old timer stopped replacement: %+v", calls)
	}
	mu.Unlock()
	m.reconcile(map[*gamepad.Pad]uint8{})
	mu.Lock()
	if len(calls) != 4 || calls[3].left != 0 || calls[3].right != 0 {
		t.Errorf("disconnect did not stop motors: %+v", calls)
	}
	mu.Unlock()
	m.play(1, pad, protocol.Vibration{LeftMotor: 0.7, Duration: time.Second})
	m.close()
	m.play(1, pad, protocol.Vibration{LeftMotor: 1, Duration: time.Second})
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 6 || calls[5].left != 0 || calls[5].right != 0 {
		t.Errorf("close did not stop motors: %+v", calls)
	}
}

func TestRumbleExpires(t *testing.T) {
	stopped := make(chan struct{}, 1)
	m := newRumbleManager(func(_ *gamepad.Pad, left, right float64) {
		if left == 0 && right == 0 {
			stopped <- struct{}{}
		}
	})
	defer m.close()
	m.play(0, new(gamepad.Pad), protocol.Vibration{LeftMotor: 1, Duration: 20 * time.Millisecond})
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("motors did not stop at end of effect")
	}
}

func TestDisabledVibrationDoesNotRegisterFeedback(t *testing.T) {
	if vibrationFeedback(nil, false) != nil {
		t.Fatal("disabled vibration returned a feedback channel")
	}
}

type feedbackTestSender struct{ added chan uint8 }

func (*feedbackTestSender) Init(uint8) error                      { return nil }
func (*feedbackTestSender) SendGamepads([]protocol.Gamepad) error { return nil }
func (s *feedbackTestSender) GamepadChanged(index uint8, added bool) error {
	if added {
		select {
		case s.added <- index:
		default:
		}
	}
	return nil
}

func TestFeedbackUsesStreamingSlotAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	close(ready)
	feedback := newVibrationMailbox()
	calls := make(chan motorCall, 3)
	pad := new(gamepad.Pad)
	sender := &feedbackTestSender{added: make(chan uint8, 1)}
	done := make(chan error, 1)
	go func() {
		done <- runWithFeedback(ctx, sender, testInputChannel{}, ready, &Keyboard{}, 0, 60,
			func() []*gamepad.Pad { return []*gamepad.Pad{pad} }, nil, nil, feedback,
			func(p *gamepad.Pad, left, right float64) { calls <- motorCall{p, left, right} })
	}()
	select {
	case <-sender.added:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("pad was not announced")
	}
	feedback.offer(vibrationMessage{value: protocol.Vibration{Index: 0, LeftMotor: 1, Duration: time.Millisecond}, received: time.Now().Add(-time.Second)})
	select {
	case call := <-calls:
		cancel()
		t.Fatalf("expired feedback activated motors: %+v", call)
	case <-time.After(30 * time.Millisecond):
	}
	feedback.offer(vibrationMessage{value: protocol.Vibration{Index: 0, LeftMotor: 0.8, RightMotor: 0.3, Duration: time.Second}, received: time.Now()})
	select {
	case call := <-calls:
		if call.pad != pad || call.left != 0.8 || call.right != 0.3 {
			t.Errorf("unexpected motor call: %+v", call)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("feedback was not applied")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case call := <-calls:
		if call.pad != pad || call.left != 0 || call.right != 0 {
			t.Errorf("motors not stopped: %+v", call)
		}
	default:
		t.Fatal("cancel did not stop motors")
	}
}

type mutableInputChannel struct{ closed atomic.Bool }

func (c *mutableInputChannel) ReadyState() webrtc.DataChannelState {
	if c.closed.Load() {
		return webrtc.DataChannelStateClosed
	}
	return webrtc.DataChannelStateOpen
}

func TestClosedInputStopsVibration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	close(ready)
	feedback := newVibrationMailbox()
	calls := make(chan motorCall, 4)
	pad := new(gamepad.Pad)
	sender := &feedbackTestSender{added: make(chan uint8, 1)}
	input := &mutableInputChannel{}
	done := make(chan error, 1)
	go func() {
		done <- runWithFeedback(ctx, sender, input, ready, &Keyboard{}, 0, 60,
			func() []*gamepad.Pad { return []*gamepad.Pad{pad} }, nil, nil, feedback,
			func(p *gamepad.Pad, left, right float64) { calls <- motorCall{p, left, right} })
	}()
	select {
	case <-sender.added:
	case <-time.After(time.Second):
		t.Fatal("pad was not announced")
	}
	feedback.offer(vibrationMessage{value: protocol.Vibration{Index: 0, LeftMotor: 1, Duration: time.Second}, received: time.Now()})
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("motors did not start")
	}
	input.closed.Store(true)
	select {
	case call := <-calls:
		if call.left != 0 || call.right != 0 {
			t.Fatalf("motors not stopped: %+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("closed channel did not stop motors")
	}
	feedback.offer(vibrationMessage{value: protocol.Vibration{Index: 0, LeftMotor: 1, Duration: time.Second}, received: time.Now()})
	select {
	case call := <-calls:
		t.Fatalf("closed channel restarted motors: %+v", call)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestVibrationMailboxKeepsLatestPerController(t *testing.T) {
	m := newVibrationMailbox()
	for i := 0; i < 1000; i++ {
		m.offer(vibrationMessage{value: protocol.Vibration{Index: 0, LeftMotor: 1}, received: time.Now()})
	}
	m.offer(vibrationMessage{value: protocol.Vibration{Index: 1, RightMotor: 0.5}, received: time.Now()})
	m.offer(vibrationMessage{value: protocol.Vibration{Index: 0}, received: time.Now()})
	m.offer(vibrationMessage{value: protocol.Vibration{Index: 9, LeftMotor: 1}, received: time.Now()})
	select {
	case <-m.ready:
	default:
		t.Fatal("missing notification")
	}
	got := m.take()
	if len(got) != 2 || got[0].value.Index != 0 || got[0].value.LeftMotor != 0 || got[1].value.Index != 1 || got[1].value.RightMotor != 0.5 {
		t.Fatalf("latest feedback lost: %+v", got)
	}
	if len(m.take()) != 0 {
		t.Fatal("mailbox did not clear")
	}
}

func TestCheckedRumbleReportsBackendFailure(t *testing.T) {
	pad := new(gamepad.Pad)
	calls := 0
	succeed := false
	m := newCheckedRumbleManager(func(_ *gamepad.Pad, left, right float64) bool {
		calls++
		return succeed
	})
	v := protocol.Vibration{LeftMotor: 0.5, RightMotor: 0.5, Duration: time.Hour}
	if m.play(0, pad, v) {
		t.Fatal("accepted failed motor request")
	}
	if len(m.effects) != 0 || calls != 1 {
		t.Fatal("failed effect was retained")
	}
	succeed = true
	if !m.play(0, pad, v) {
		t.Fatal("rejected successful motor request")
	}
	m.close()
	if calls != 3 || m.play(0, pad, v) {
		t.Fatalf("cleanup failed, calls=%d", calls)
	}
}
