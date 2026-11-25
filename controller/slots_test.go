package controller

import (
	"github.com/egoist/mygo/plugins/game/gamepad"
	"reflect"
	"testing"
)

func TestSlotStability(t *testing.T) {
	var tracker slotTracker
	p1, p2, p3 := new(gamepad.Pad), new(gamepad.Pad), new(gamepad.Pad)
	added, removed := tracker.update([]*gamepad.Pad{p1, p2}, false)
	if !reflect.DeepEqual(added, []uint8{0, 1}) || len(removed) != 0 {
		t.Fatal(added, removed)
	}
	added, removed = tracker.update([]*gamepad.Pad{p2}, false)
	if len(added) != 0 || !reflect.DeepEqual(removed, []uint8{0}) || tracker.pads[p2] != 1 {
		t.Fatal(added, removed, tracker.pads)
	}
	added, removed = tracker.update([]*gamepad.Pad{p2, p3}, false)
	if !reflect.DeepEqual(added, []uint8{0}) || len(removed) != 0 || tracker.pads[p2] != 1 || tracker.pads[p3] != 0 {
		t.Fatal(added, removed, tracker.pads)
	}
	added, removed = tracker.update([]*gamepad.Pad{p2}, true)
	if len(added) != 0 || len(removed) != 0 {
		t.Fatal("keyboard should retain slot 0", added, removed)
	}
	added, removed = tracker.update(nil, true)
	if len(added) != 0 || !reflect.DeepEqual(removed, []uint8{1}) {
		t.Fatal(added, removed)
	}
	added, removed = tracker.update(nil, false)
	if len(added) != 0 || !reflect.DeepEqual(removed, []uint8{0}) {
		t.Fatal(added, removed)
	}
}
func TestSlotLimit(t *testing.T) {
	var tracker slotTracker
	pads := make([]*gamepad.Pad, 5)
	for i := range pads {
		pads[i] = new(gamepad.Pad)
	}
	added, _ := tracker.update(pads, false)
	if len(added) != 4 || len(tracker.pads) != 4 {
		t.Fatal(added, tracker.pads)
	}
}

func TestReplacementInSamePollResetsSlot(t *testing.T) {
	var tracker slotTracker
	first, second := new(gamepad.Pad), new(gamepad.Pad)
	tracker.update([]*gamepad.Pad{first}, false)
	added, removed := tracker.update([]*gamepad.Pad{second}, false)
	if !reflect.DeepEqual(removed, []uint8{0}) || !reflect.DeepEqual(added, []uint8{0}) {
		t.Fatal(added, removed)
	}
}
