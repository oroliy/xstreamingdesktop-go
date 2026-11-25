package protocol

import (
	"testing"
	"time"
)

func TestParseVibration(t *testing.T) {
	data := []byte{128, 0, 0, 2, 100, 25, 200, 0, 0xe8, 0x03, 0x34, 0x12, 3}
	got, err := ParseVibration(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 2 || got.LeftMotor != 1 || got.RightMotor != 0.25 || got.LeftTrigger != 1 || got.RightTrigger != 0 || got.Duration != 100*time.Millisecond || got.Delay != 0x1234 || got.Repeat != 3 {
		t.Fatalf("unexpected feedback: %+v", got)
	}
}

func TestParseVibrationRejectsInvalidReports(t *testing.T) {
	for _, data := range [][]byte{nil, make([]byte, 12), {1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, {128, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}} {
		if _, err := ParseVibration(data); err == nil {
			t.Fatalf("accepted %v", data)
		}
	}
}
