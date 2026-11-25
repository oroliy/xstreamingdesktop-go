//go:build !windows

package media

import "os/exec"

func configureMediaProcess(*exec.Cmd) {}
