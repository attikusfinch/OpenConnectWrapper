//go:build !windows

package desktop

import (
	"os/exec"
	"runtime"
)

func ActivateExisting(_ string) bool { return false }

func Open(url string, _ string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
