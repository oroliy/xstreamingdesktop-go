//go:build windows && (amd64 || arm64)

package controller

import (
	"sync"
	"unsafe"

	"github.com/egoist/mygo/plugins/game/gamepad"
	"golang.org/x/sys/windows"
)

var xinputVibration struct {
	once sync.Once
	proc *windows.LazyProc
}

type xinputMotors struct{ left, right uint16 }

func setPadVibration(pad *gamepad.Pad, left, right float64) {
	_ = setPadVibrationChecked(pad, left, right)
}

func setPadVibrationChecked(pad *gamepad.Pad, left, right float64) bool {
	// A disconnected MyGo pad may have had its XInput slot reassigned.
	// MyGo's Windows gamepad backend names its XInput slots this way.
	slot, ok := connectedXInputSlot(pad)
	if !ok {
		return false
	}
	xinputVibration.once.Do(func() {
		for _, name := range []string{"xinput1_4.dll", "xinput1_3.dll", "xinput9_1_0.dll"} {
			proc := windows.NewLazySystemDLL(name).NewProc("XInputSetState")
			if proc.Find() == nil {
				xinputVibration.proc = proc
				return
			}
		}
	})
	if xinputVibration.proc == nil {
		return false
	}
	motors := xinputMotors{uint16(left * 65535), uint16(right * 65535)}
	result, _, _ := xinputVibration.proc.Call(uintptr(slot), uintptr(unsafe.Pointer(&motors)))
	return result == 0
}
