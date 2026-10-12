//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
)

func launchApp(app, dir string) error { return start(exec.Command(app, "--config-dir", dir)) }
func openURL(url string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	return start(exec.Command(command, url))
}
func start(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
