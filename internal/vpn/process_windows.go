//go:build windows

package vpn

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
}

func processIdentity(pid int) (uint64, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return 0, os.ErrNotExist
	}
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	if exited.HighDateTime != 0 || exited.LowDateTime != 0 {
		return 0, os.ErrNotExist
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}
