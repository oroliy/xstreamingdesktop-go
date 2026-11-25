package protocol

import (
	"encoding/binary"
	"errors"
	"time"
)

// Vibration is a four-motor controller feedback report from the input channel.
// Motor values are normalized to 0..1. Delay and Repeat are retained as raw
// protocol fields because their playback semantics are not yet validated.
type Vibration struct {
	Index                                            uint8
	LeftMotor, RightMotor, LeftTrigger, RightTrigger float64
	Duration                                         time.Duration
	Delay                                            uint16
	Repeat                                           uint8
}

// ParseVibration decodes the report format used by xstreaming-player.
func ParseVibration(data []byte) (Vibration, error) {
	if len(data) < 13 || data[0] != 128 || data[2] != 0 {
		return Vibration{}, errors.New("invalid or unsupported vibration report")
	}
	motor := func(v byte) float64 { return float64(min(v, 100)) / 100 }
	return Vibration{
		Index: data[3], LeftMotor: motor(data[4]), RightMotor: motor(data[5]),
		LeftTrigger: motor(data[6]), RightTrigger: motor(data[7]),
		Duration: time.Duration(binary.LittleEndian.Uint16(data[8:10])) * time.Millisecond / 10,
		Delay:    binary.LittleEndian.Uint16(data[10:12]), Repeat: data[12],
	}, nil
}
