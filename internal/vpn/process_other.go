//go:build !windows

package vpn

import (
	"os"
	"os/exec"
)

func configureCommand(_ *exec.Cmd) {}

func processIdentity(_ int) (uint64, error) { return 0, os.ErrNotExist }
