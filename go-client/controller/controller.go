// Package controller maps MyGo gamepad state to Xbox streaming input.
package controller

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/game/gamepad"
	"github.com/oroliy/xstreamingdesktop-go/go-client/peer"
	"github.com/oroliy/xstreamingdesktop-go/go-client/protocol"
	"github.com/pion/webrtc/v4"
)

func Map(s gamepad.State) protocol.Gamepad {
	g := protocol.Gamepad{}
	for _, b := range []struct {
		source gamepad.Button
		target protocol.Buttons
	}{{gamepad.South, protocol.A}, {gamepad.East, protocol.B}, {gamepad.West, protocol.X}, {gamepad.North, protocol.Y}, {gamepad.LeftShoulder, protocol.LeftShoulder}, {gamepad.RightShoulder, protocol.RightShoulder}, {gamepad.Back, protocol.View}, {gamepad.Start, protocol.Menu}, {gamepad.LeftStickButton, protocol.LeftThumb}, {gamepad.RightStickButton, protocol.RightThumb}, {gamepad.Up, protocol.DPadUp}, {gamepad.Down, protocol.DPadDown}, {gamepad.Left, protocol.DPadLeft}, {gamepad.Right, protocol.DPadRight}, {gamepad.Guide, protocol.Nexus}} {
		if s.Pressed(b.source) {
			g.Buttons |= b.target
		}
	}
	g.LeftX = float64(s.Axis(gamepad.LeftX))
	g.LeftY = float64(s.Axis(gamepad.LeftY))
	g.RightX = float64(s.Axis(gamepad.RightX))
	g.RightY = float64(s.Axis(gamepad.RightY))
	g.LeftTrigger = float64(s.Value(gamepad.LeftTrigger))
	g.RightTrigger = float64(s.Value(gamepad.RightTrigger))
	return g
}

// ApplyDeadzone removes small stick movements and rescales the remaining range.
func ApplyDeadzone(x, y, deadzone float64) (float64, float64) {
	if deadzone <= 0 {
		return x, y
	}
	magnitude := math.Hypot(x, y)
	if magnitude <= deadzone {
		return 0, 0
	}
	scale := math.Min((magnitude-deadzone)/(1-deadzone), 1) / magnitude
	return x * scale, y * scale
}

// Run sends up to four connected controllers until ctx is canceled.
// On disconnect it sends a neutral frame to release held buttons.
func Run(ctx context.Context, sender *peer.InputSender, channel *webrtc.DataChannel, ready <-chan struct{}, keyboard *Keyboard, deadzone float64, pollingRate int, vibration bool, onError func(error), onRecovery func()) error {
	feedback := vibrationFeedback(channel, vibration)
	return runWithFeedback(ctx, sender, channel, ready, keyboard, deadzone, pollingRate, gamepad.Pads, onError, onRecovery, feedback, setPadVibration)
}

type vibrationMessage struct {
	value    protocol.Vibration
	received time.Time
}

type vibrationMailbox struct {
	mu      sync.Mutex
	ready   chan struct{}
	pending [4]*vibrationMessage
}

func newVibrationMailbox() *vibrationMailbox { return &vibrationMailbox{ready: make(chan struct{}, 1)} }

func (m *vibrationMailbox) offer(message vibrationMessage) {
	if message.value.Index >= uint8(len(m.pending)) {
		return
	}
	m.mu.Lock()
	m.pending[message.value.Index] = &message
	m.mu.Unlock()
	select {
	case m.ready <- struct{}{}:
	default:
	}
}

func (m *vibrationMailbox) take() []vibrationMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	var messages []vibrationMessage
	for i, item := range m.pending {
		if item != nil {
			messages = append(messages, *item)
			m.pending[i] = nil
		}
	}
	return messages
}

func vibrationFeedback(channel *webrtc.DataChannel, enabled bool) *vibrationMailbox {
	if !enabled {
		return nil
	}
	feedback := newVibrationMailbox()
	channel.OnMessage(func(msg webrtc.DataChannelMessage) {
		if msg.IsString {
			return
		}
		v, err := protocol.ParseVibration(msg.Data)
		if err == nil {
			feedback.offer(vibrationMessage{value: v, received: time.Now()})
		}
	})
	return feedback
}

type inputSender interface {
	Init(uint8) error
	SendGamepads([]protocol.Gamepad) error
	GamepadChanged(uint8, bool) error
}

type inputChannel interface {
	ReadyState() webrtc.DataChannelState
}

func run(ctx context.Context, sender inputSender, channel inputChannel, ready <-chan struct{}, keyboard *Keyboard, deadzone float64, pollingRate int, pads func() []*gamepad.Pad, onError func(error), onRecovery func()) error {
	return runWithFeedback(ctx, sender, channel, ready, keyboard, deadzone, pollingRate, pads, onError, onRecovery, nil, nil)
}

func runWithFeedback(ctx context.Context, sender inputSender, channel inputChannel, ready <-chan struct{}, keyboard *Keyboard, deadzone float64, pollingRate int, pads func() []*gamepad.Pad, onError func(error), onRecovery func(), feedback *vibrationMailbox, setMotor func(*gamepad.Pad, float64, float64)) error {
	select {
	case <-ctx.Done():
		return nil
	case <-ready:
	}
	if pollingRate != 60 && pollingRate != 125 && pollingRate != 250 {
		pollingRate = 60
	}
	ticker := time.NewTicker(time.Second / time.Duration(pollingRate))
	defer ticker.Stop()
	initialized := false
	slots := slotTracker{}
	rumble := newRumbleManager(setMotor)
	defer rumble.close()
	var feedbackReady <-chan struct{}
	if feedback != nil {
		feedbackReady = feedback.ready
	}
	failed := false
	transitionDelayed := false
	report := func(err error) {
		if !failed && onError != nil {
			onError(err)
		}
		failed = true
	}
	recovered := func() {
		if failed && onRecovery != nil {
			onRecovery()
		}
		failed = false
	}
	sendTransition := func(send func() error) bool {
		for ctx.Err() == nil {
			if err := send(); err == nil {
				recovered()
				return true
			} else {
				transitionDelayed = true
				report(err)
			}
			if !waitRetry(ctx) {
				return false
			}
		}
		return false
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-feedbackReady:
			if channel.ReadyState() != webrtc.DataChannelStateOpen {
				feedback.take()
				continue
			}
			for _, message := range feedback.take() {
				v := message.value
				if v.Duration > 0 {
					v.Duration -= time.Since(message.received)
					if v.Duration <= 0 {
						continue
					}
				}
				for pad, index := range slots.pads {
					if index == v.Index {
						rumble.play(v.Index, pad, v)
						break
					}
				}
			}
		case <-ticker.C:
			transitionDelayed = false
			state := channel.ReadyState()
			if state == webrtc.DataChannelStateClosing || state == webrtc.DataChannelStateClosed {
				rumble.clear()
				report(errors.New("controller input channel closed"))
				if !waitRetry(ctx) {
					return nil
				}
				continue
			}
			if state != webrtc.DataChannelStateOpen {
				continue
			}
			if !initialized {
				if !sendTransition(func() error { return sender.Init(0) }) {
					return nil
				}
				initialized = true
			}
			pads := pads()
			enabled, virtual := keyboard.Snapshot()
			added, removed := slots.update(pads, enabled)
			rumble.reconcile(slots.pads)
			for _, index := range removed {
				if !sendTransition(func() error { return sender.SendGamepads([]protocol.Gamepad{{Index: index}}) }) {
					return nil
				}
				if !sendTransition(func() error { return sender.GamepadChanged(index, false) }) {
					return nil
				}
			}
			for _, index := range added {
				if !sendTransition(func() error { return sender.GamepadChanged(index, true) }) {
					return nil
				}
			}
			// State may have changed while a transition was being retried.
			if transitionDelayed {
				continue
			}
			frames := make([]protocol.Gamepad, 0, 4)
			for _, pad := range pads {
				index, ok := slots.pads[pad]
				if !ok {
					continue
				}
				state := Map(pad.State())
				state.LeftX, state.LeftY = ApplyDeadzone(state.LeftX, state.LeftY, deadzone)
				state.RightX, state.RightY = ApplyDeadzone(state.RightX, state.RightY, deadzone)
				state.Index = index
				if enabled && index == 0 {
					state = merge(state, virtual)
				}
				frames = append(frames, state)
			}
			if enabled {
				hasSlotZero := false
				for _, state := range frames {
					if state.Index == 0 {
						hasSlotZero = true
						break
					}
				}
				if !hasSlotZero {
					frames = append(frames, virtual)
				}
			}
			if len(frames) > 0 {
				if err := sender.SendGamepads(frames); err != nil {
					report(err)
					if !waitRetry(ctx) {
						return nil
					}
				} else {
					recovered()
				}
			}
		}
	}
}

func waitRetry(ctx context.Context) bool {
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
