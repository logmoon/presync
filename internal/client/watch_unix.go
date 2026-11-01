//go:build !windows

package client

import (
	"os"
	"os/exec"
	"syscall"
)

func configureWatcherCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}

func sendWatcherStop(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}

func watcherProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
