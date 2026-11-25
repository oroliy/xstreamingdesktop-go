package controller

import "github.com/egoist/mygo/plugins/game/gamepad"

// slotTracker retains an index for each connected device until it disconnects.
type slotTracker struct {
	pads   map[*gamepad.Pad]uint8
	active [4]bool
}

func (s *slotTracker) update(pads []*gamepad.Pad, keyboard bool) (added, removed []uint8) {
	if s.pads == nil {
		s.pads = make(map[*gamepad.Pad]uint8)
	}
	var previous [4]*gamepad.Pad
	for pad, slot := range s.pads {
		previous[slot] = pad
	}
	present := make(map[*gamepad.Pad]bool, len(pads))
	for _, pad := range pads {
		present[pad] = true
	}
	for pad := range s.pads {
		if !present[pad] {
			delete(s.pads, pad)
		}
	}
	var occupied [4]bool
	for _, slot := range s.pads {
		occupied[slot] = true
	}
	for _, pad := range pads {
		if _, ok := s.pads[pad]; ok {
			continue
		}
		for i := range occupied {
			if !occupied[i] {
				s.pads[pad] = uint8(i)
				occupied[i] = true
				break
			}
		}
	}
	var current [4]*gamepad.Pad
	for pad, slot := range s.pads {
		current[slot] = pad
	}
	desired := occupied
	if keyboard {
		desired[0] = true
	}
	for i, want := range desired {
		if s.active[i] && want && previous[i] != nil && current[i] != nil && previous[i] != current[i] {
			removed = append(removed, uint8(i))
			added = append(added, uint8(i))
			continue
		}
		if want && !s.active[i] {
			added = append(added, uint8(i))
		}
		if !want && s.active[i] {
			removed = append(removed, uint8(i))
		}
	}
	s.active = desired
	return
}
