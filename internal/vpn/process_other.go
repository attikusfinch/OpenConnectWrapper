//go:build !windows

package vpn

import "os/exec"

func configureCommand(_ *exec.Cmd) {}
