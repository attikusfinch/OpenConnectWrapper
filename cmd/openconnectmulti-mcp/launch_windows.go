//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"syscall"
)

func shellOpen(target, args string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL)
}

func launchApp(app, dir string) error {
	return shellOpen(app, "--config-dir "+syscall.EscapeArg(dir))
}

func openURL(url string) error { return shellOpen(url, "") }
