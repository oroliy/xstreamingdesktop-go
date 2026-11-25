//go:build !windows || (!amd64 && !arm64)

package controller

import "github.com/egoist/mygo/plugins/game/gamepad"

func setPadVibration(*gamepad.Pad, float64, float64) {}

func setPadVibrationChecked(*gamepad.Pad, float64, float64) bool { return false }
