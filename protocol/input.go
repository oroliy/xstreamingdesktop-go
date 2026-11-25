package protocol

import (
	"encoding/binary"
	"errors"
	"math"
)

// Buttons uses the 16-bit gamepad button mask in the Xbox input protocol.
type Buttons uint16

const (
	Nexus Buttons = 1 << (iota + 1)
	Menu
	View
	A
	B
	X
	Y
	DPadUp
	DPadDown
	DPadLeft
	DPadRight
	LeftShoulder
	RightShoulder
	LeftThumb
	RightThumb
)

type Gamepad struct {
	Index                        uint8
	Buttons                      Buttons
	LeftX, LeftY, RightX, RightY float64
	LeftTrigger, RightTrigger    float64
	// Set PhysicalityOverride when the input comes from a virtual controller.
	PhysicalityOverride             bool
	Physicality, VirtualPhysicality uint32
}

func header(report uint16, seq uint32, milliseconds float64, size int) []byte {
	b := make([]byte, size)
	binary.LittleEndian.PutUint16(b, report)
	binary.LittleEndian.PutUint32(b[2:], seq)
	binary.LittleEndian.PutUint64(b[6:], math.Float64bits(milliseconds))
	return b
}
func ClientMetadata(seq uint32, milliseconds float64, maxTouchpoints uint8) []byte {
	b := header(8, seq, milliseconds, 15)
	b[14] = maxTouchpoints
	return b
}
func clamp(value, min, max float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return math.Max(min, math.Min(max, value))
}
func axis(v float64) uint16    { return uint16(int16(clamp(v, -1, 1) * 32767)) }
func trigger(v float64) uint16 { return uint16(clamp(v, 0, 1) * 65535) }
func physical(g Gamepad) uint32 {
	var mask uint32
	for _, m := range []struct {
		button Buttons
		bit    uint32
	}{{DPadUp, 1}, {DPadDown, 2}, {DPadLeft, 4}, {DPadRight, 8}, {Menu, 16}, {View, 32}, {LeftThumb, 64}, {RightThumb, 128}, {LeftShoulder, 256}, {RightShoulder, 512}, {Nexus, 1024}, {A, 4096}, {B, 8192}, {X, 16384}, {Y, 32768}} {
		if g.Buttons&m.button != 0 {
			mask |= m.bit
		}
	}
	if g.LeftTrigger > 0 {
		mask |= 1 << 16
	}
	if g.RightTrigger > 0 {
		mask |= 1 << 17
	}
	if g.LeftX != 0 || g.LeftY != 0 {
		mask |= 3 << 18
	}
	if g.RightX != 0 || g.RightY != 0 {
		mask |= 3 << 20
	}
	return mask
}

// GamepadPacket encodes one or more frames. Timestamps are monotonic milliseconds.
func GamepadPacket(seq uint32, milliseconds float64, frames []Gamepad) ([]byte, error) {
	if len(frames) == 0 || len(frames) > 255 {
		return nil, errors.New("gamepad frame count must be 1 to 255")
	}
	b := header(2, seq, milliseconds, 15+23*len(frames))
	b[14] = uint8(len(frames))
	o := 15
	for _, g := range frames {
		b[o] = g.Index
		o++
		binary.LittleEndian.PutUint16(b[o:], uint16(g.Buttons))
		binary.LittleEndian.PutUint16(b[o+2:], axis(g.LeftX))
		binary.LittleEndian.PutUint16(b[o+4:], axis(-g.LeftY))
		binary.LittleEndian.PutUint16(b[o+6:], axis(g.RightX))
		binary.LittleEndian.PutUint16(b[o+8:], axis(-g.RightY))
		binary.LittleEndian.PutUint16(b[o+10:], trigger(g.LeftTrigger))
		binary.LittleEndian.PutUint16(b[o+12:], trigger(g.RightTrigger))
		p := physical(g)
		if g.PhysicalityOverride {
			p = g.Physicality
		}
		binary.LittleEndian.PutUint32(b[o+14:], p)
		binary.LittleEndian.PutUint32(b[o+18:], g.VirtualPhysicality)
		o += 22
	}
	return b, nil
}
