package controller

import (
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/go-client/protocol"
)

// Keyboard safely shares the current virtual gamepad state with the sender.
type Keyboard struct {
	mu         sync.RWMutex
	enabled    bool
	state      protocol.Gamepad
	nexusUntil time.Time
}

func (k *Keyboard) Set(enabled bool, state protocol.Gamepad) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.enabled = enabled
	if enabled {
		k.state = state
	} else {
		k.state = protocol.Gamepad{}
	}
}
func (k *Keyboard) Snapshot() (bool, protocol.Gamepad) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	state := k.state
	pulsing := time.Now().Before(k.nexusUntil)
	if pulsing {
		state.Buttons |= protocol.Nexus
	}
	return k.enabled || pulsing, state
}

// PressNexus temporarily adds a Guide press to the first controller.
func (k *Keyboard) PressNexus(duration time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nexusUntil = time.Now().Add(duration)
}

// ClearNexus releases a pending on-screen press when the stream ends.
func (k *Keyboard) ClearNexus() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nexusUntil = time.Time{}
}

// MapKeys uses the original client's default keyboard-to-gamepad mapping.
func MapKeys(down func(ui.Key) bool) protocol.Gamepad {
	var g protocol.Gamepad
	for _, v := range []struct {
		key    ui.Key
		button protocol.Buttons
	}{{ui.KeyUp, protocol.DPadUp}, {ui.KeyDown, protocol.DPadDown}, {ui.KeyLeft, protocol.DPadLeft}, {ui.KeyRight, protocol.DPadRight}, {ui.KeyK, protocol.A}, {ui.KeyEnter, protocol.A}, {ui.KeyL, protocol.B}, {ui.KeyBackspace, protocol.B}, {ui.KeyJ, protocol.X}, {ui.KeyI, protocol.Y}, {ui.Key2, protocol.LeftShoulder}, {ui.Key3, protocol.RightShoulder}, {ui.Key5, protocol.LeftThumb}, {ui.Key6, protocol.RightThumb}, {ui.KeyV, protocol.View}, {ui.KeyM, protocol.Menu}, {ui.KeyN, protocol.Nexus}} {
		if down(v.key) {
			g.Buttons |= v.button
		}
	}
	axis := func(negative, positive ui.Key) float64 {
		value := 0.0
		if down(negative) {
			value--
		}
		if down(positive) {
			value++
		}
		return value
	}
	g.LeftX = axis(ui.KeyA, ui.KeyD)
	g.LeftY = axis(ui.KeyW, ui.KeyS)
	g.RightX = axis(ui.KeyF, ui.KeyH)
	g.RightY = axis(ui.KeyT, ui.KeyG)
	if down(ui.Key1) {
		g.LeftTrigger = 1
	}
	if down(ui.Key4) {
		g.RightTrigger = 1
	}
	return g
}
func merge(base, virtual protocol.Gamepad) protocol.Gamepad {
	base.Buttons |= virtual.Buttons
	if virtual.LeftX != 0 {
		base.LeftX = virtual.LeftX
	}
	if virtual.LeftY != 0 {
		base.LeftY = virtual.LeftY
	}
	if virtual.RightX != 0 {
		base.RightX = virtual.RightX
	}
	if virtual.RightY != 0 {
		base.RightY = virtual.RightY
	}
	base.LeftTrigger = max(base.LeftTrigger, virtual.LeftTrigger)
	base.RightTrigger = max(base.RightTrigger, virtual.RightTrigger)
	return base
}
