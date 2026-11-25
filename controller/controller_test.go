package controller

import (
	"context"
	"errors"
	"github.com/egoist/mygo/plugins/game/gamepad"
	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/protocol"
	"github.com/pion/webrtc/v4"
	"math"
	"testing"
	"time"
)

type testInputChannel struct{}

func (testInputChannel) ReadyState() webrtc.DataChannelState { return webrtc.DataChannelStateOpen }

type closedInputChannel struct{}

func (closedInputChannel) ReadyState() webrtc.DataChannelState { return webrtc.DataChannelStateClosed }

type retryInputSender struct {
	keyboard                           *Keyboard
	initCalls, changeCalls, frameCalls int
	result                             chan protocol.Gamepad
}

func (s *retryInputSender) Init(uint8) error {
	s.initCalls++
	if s.initCalls == 1 {
		return errors.New("initialization unavailable")
	}
	return nil
}
func (s *retryInputSender) GamepadChanged(_ uint8, _ bool) error {
	s.changeCalls++
	if s.changeCalls == 1 {
		return errors.New("control unavailable")
	}
	return nil
}
func (s *retryInputSender) SendGamepads(frames []protocol.Gamepad) error {
	s.frameCalls++
	if s.frameCalls == 1 {
		s.keyboard.Set(true, protocol.Gamepad{Buttons: protocol.B})
		return errors.New("input unavailable")
	}
	select {
	case s.result <- frames[0]:
	default:
	}
	return nil
}

func TestInputRetriesTransitionsAndRefreshesHeldState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ready := make(chan struct{})
	close(ready)
	keyboard := &Keyboard{}
	keyboard.Set(true, protocol.Gamepad{Buttons: protocol.A})
	sender := &retryInputSender{keyboard: keyboard, result: make(chan protocol.Gamepad, 1)}
	errorsReported, recoveries := 0, 0
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, sender, testInputChannel{}, ready, keyboard, 0, 60, func() []*gamepad.Pad { return nil }, func(error) { errorsReported++ }, func() { recoveries++ })
	}()
	select {
	case frame := <-sender.result:
		if frame.Buttons != protocol.B {
			t.Fatalf("replayed stale controller state: %+v", frame)
		}
	case <-ctx.Done():
		t.Fatal("controller did not recover")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if sender.initCalls != 2 || sender.changeCalls != 2 || sender.frameCalls < 2 || errorsReported != 3 || recoveries != 3 {
		t.Fatalf("init=%d changes=%d frames=%d errors=%d recoveries=%d", sender.initCalls, sender.changeCalls, sender.frameCalls, errorsReported, recoveries)
	}
}

type delayedTransitionSender struct {
	keyboard *Keyboard
	changes  int
	result   chan protocol.Gamepad
}

func (*delayedTransitionSender) Init(uint8) error { return nil }
func (s *delayedTransitionSender) GamepadChanged(uint8, bool) error {
	s.changes++
	if s.changes == 1 {
		s.keyboard.Set(true, protocol.Gamepad{Buttons: protocol.B})
		return errors.New("temporary control failure")
	}
	return nil
}
func (s *delayedTransitionSender) SendGamepads(frames []protocol.Gamepad) error {
	select {
	case s.result <- frames[0]:
	default:
	}
	return nil
}

func TestInputRefreshesStateAfterDelayedTransition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ready := make(chan struct{})
	close(ready)
	keyboard := &Keyboard{}
	keyboard.Set(true, protocol.Gamepad{Buttons: protocol.A})
	sender := &delayedTransitionSender{keyboard: keyboard, result: make(chan protocol.Gamepad, 1)}
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, sender, testInputChannel{}, ready, keyboard, 0, 60, func() []*gamepad.Pad { return nil }, nil, nil)
	}()
	select {
	case frame := <-sender.result:
		if frame.Buttons != protocol.B {
			t.Fatalf("sent stale state after delayed transition: %+v", frame)
		}
	case <-ctx.Done():
		t.Fatal("controller did not resume after transition")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestInputRetryStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitRetry(ctx) {
		t.Fatal("retry continued after cancellation")
	}
}

func TestClosedInputChannelReportsInterruption(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ready := make(chan struct{})
	close(ready)
	reported := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, &retryInputSender{}, closedInputChannel{}, ready, &Keyboard{}, 0, 60, func() []*gamepad.Pad { return nil }, func(err error) { reported <- err }, nil)
	}()
	select {
	case <-reported:
	case <-ctx.Done():
		t.Fatal("closed input channel was not reported")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMap(t *testing.T) {
	var s gamepad.State
	s.Buttons[gamepad.South] = 1
	s.Buttons[gamepad.Up] = 1
	s.Buttons[gamepad.LeftTrigger] = 0.5
	s.Axes[gamepad.LeftY] = -0.75
	g := Map(s)
	if g.Buttons != protocol.A|protocol.DPadUp || g.LeftTrigger != 0.5 || g.LeftY != -0.75 {
		t.Fatalf("%+v", g)
	}
}

func TestKeyboardMappingAndMerge(t *testing.T) {
	pressed := map[ui.Key]bool{ui.KeyW: true, ui.KeyK: true, ui.Key4: true, ui.KeyRight: true}
	v := MapKeys(func(k ui.Key) bool { return pressed[k] })
	if v.LeftY != -1 || v.Buttons != protocol.A|protocol.DPadRight || v.RightTrigger != 1 {
		t.Fatalf("%+v", v)
	}
	base := protocol.Gamepad{Buttons: protocol.B, LeftX: 0.4}
	out := merge(base, v)
	if out.Buttons != protocol.A|protocol.B|protocol.DPadRight || out.LeftX != 0.4 || out.LeftY != -1 {
		t.Fatalf("%+v", out)
	}
	var k Keyboard
	k.Set(true, v)
	enabled, _ := k.Snapshot()
	if !enabled {
		t.Fatal("not enabled")
	}
	k.Set(false, v)
	enabled, v = k.Snapshot()
	if enabled || v.Buttons != 0 {
		t.Fatal("keyboard not cleared")
	}
}

func TestStickDeadzone(t *testing.T) {
	if x, y := ApplyDeadzone(0.03, 0.04, 0.1); x != 0 || y != 0 {
		t.Fatalf("center drift: %v %v", x, y)
	}
	if x, y := ApplyDeadzone(0.55, 0, 0.1); math.Abs(x-0.5) > 1e-9 || y != 0 {
		t.Fatalf("scaled stick: %v %v", x, y)
	}
	if x, y := ApplyDeadzone(0.03, 0.04, 0); x != 0.03 || y != 0.04 {
		t.Fatalf("disabled deadzone: %v %v", x, y)
	}
}
