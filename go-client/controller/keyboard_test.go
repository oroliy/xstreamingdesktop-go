package controller

import (
	"testing"
	"time"

	"github.com/oroliy/xstreamingdesktop-go/go-client/protocol"
)

func TestNexusPressMergesAndReleases(t *testing.T) {
	var k Keyboard
	k.PressNexus(time.Minute)
	enabled, state := k.Snapshot()
	if !enabled || state.Buttons != protocol.Nexus {
		t.Fatalf("press: enabled=%v buttons=%v", enabled, state.Buttons)
	}
	k.Set(true, protocol.Gamepad{Buttons: protocol.A})
	_, state = k.Snapshot()
	if state.Buttons != protocol.A|protocol.Nexus {
		t.Fatalf("merged buttons=%v", state.Buttons)
	}
	k.ClearNexus()
	enabled, state = k.Snapshot()
	if !enabled || state.Buttons != protocol.A {
		t.Fatalf("release: enabled=%v buttons=%v", enabled, state.Buttons)
	}
	k.Set(false, protocol.Gamepad{})
	k.PressNexus(-time.Second)
	if enabled, state := k.Snapshot(); enabled || state.Buttons != 0 {
		t.Fatalf("expired: enabled=%v buttons=%v", enabled, state.Buttons)
	}
}
